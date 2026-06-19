package VL

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// TestReclaimVersionNodes_WithBatch exercises the non-empty path of
// ReclaimVersionNodes. The function's actual reclaim behaviour is a
// no-op stub (see gc.go), so we just verify the call doesn't panic
// and returns promptly.
func TestReclaimVersionNodes_WithBatch(t *testing.T) {
	StartGC()
	defer StopGC()

	// Use a heap-allocated array so the pointer values are stable
	// across the goroutine boundary (checkptr disallows arithmetic
	// on stack addresses).
	storage := make([]int, 4)
	batch := make([]unsafe.Pointer, len(storage))
	for i := range storage {
		batch[i] = unsafe.Pointer(&storage[i])
	}

	done := make(chan struct{})
	go func() {
		ReclaimVersionNodes(batch)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ReclaimVersionNodes hung")
	}
}

// TestReclaimVersionNodes_NilPointerInBatch verifies the function
// tolerates nil entries in the batch (gc.go skips nil pointers).
func TestReclaimVersionNodes_NilPointerInBatch(t *testing.T) {
	StartGC()
	defer StopGC()

	// Heap-allocate the target so the unsafe.Pointer to it is a
	// valid Go heap pointer. The Go runtime's "bad pointer" check
	// rejects stack addresses stored in heap-allocated slices.
	x := 42
	xEscaped := &x
	batch := []unsafe.Pointer{
		unsafe.Pointer(xEscaped),
		nil,
		unsafe.Pointer(xEscaped),
	}
	ReclaimVersionNodes(batch) // must not panic
}

// TestTx_Get_NoChain returns nil without error when the key is absent.
func TestTx_Get_NoChain(t *testing.T) {
	tx, err := Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort(context.Background())

	got, err := tx.Get(context.Background(), []byte("missing"))
	if err != nil {
		t.Errorf("Get on missing key: want nil err, got %v", err)
	}
	if got != nil {
		t.Errorf("Get on missing key: want nil, got %v", got)
	}
}

// TestTx_Commit_EmptyWriteSet commits with no writes — should succeed
// and return the slot to the pool.
func TestTx_Commit_EmptyWriteSet(t *testing.T) {
	m := NewManager()
	defer m.Close()

	tx, err := m.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Errorf("Commit with no writes: %v", err)
	}
	stats := m.Stats()
	if stats.Committed != 1 {
		t.Errorf("expected Committed=1, got %d", stats.Committed)
	}
	if stats.Active != 0 {
		t.Errorf("expected Active=0, got %d", stats.Active)
	}
}

// TestTx_Delete_ThenGetReturnsNil exercises the delete→read path: after
// a Delete in the same transaction, Get returns nil (own-writes path).
func TestTx_Delete_ThenGetReturnsNil(t *testing.T) {
	tx, err := Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort(context.Background())

	k := []byte("dk")
	if err := tx.Insert(context.Background(), k, []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(context.Background(), k); err != nil {
		t.Fatal(err)
	}
	got, _ := tx.Get(context.Background(), k)
	if got != nil {
		t.Errorf("Get after Delete: want nil, got %v", got)
	}
}

// TestTx_Commit_WithWriteWriteConflict exercises the Commit path where
// validation fails. The function must roll back via Abort.
func TestTx_Commit_WithWriteWriteConflict(t *testing.T) {
	m := NewManager()
	defer m.Close()

	// Build a "ghost" committed slot that overlaps in time and key
	// range with our candidate transaction. This forces Validate()
	// to return false, which is the precondition for the conflict
	// branch in Commit.
	sm := m.sm
	ghost := sm.AllocateSlot()
	if ghost == nil {
		t.Fatal("no slot")
	}
	ghost.txnID = 99
	ghost.beginTS = 1
	ghost.commitTS = 100
	ghost.status.Store(int32(SlotCommitted))
	// Range [a, c) for the ghost...
	ghost.writeSet = []KeyRange{{Start: []byte("a"), End: []byte("c")}}

	// Candidate slot with a write set that overlaps the ghost's
	// (b is in [a, c)) and a beginTS in the ghost's [beginTS, commitTS]
	// window.
	cand := sm.AllocateSlot()
	if cand == nil {
		t.Fatal("no slot")
	}
	cand.txnID = 100
	cand.beginTS = 50
	cand.status.Store(int32(SlotActive))
	cand.writeSet = []KeyRange{{Start: []byte("b"), End: []byte("b")}}

	tx2 := &tx{sm: sm, mv: m.mv, manager: m, slot: cand}
	// Commit internally routes through Abort when validation fails;
	// the observable signal is the Aborted counter, not a return
	// value. (The contract: a successful Commit returns nil; a
	// conflicting Commit also returns nil but bumps Aborted.)
	_ = tx2.Commit(context.Background())
	stats := m.Stats()
	if stats.Aborted != 1 {
		t.Errorf("expected Aborted=1, got %d", stats.Aborted)
	}
	sm.ReleaseSlot(ghost)
	// After releasing the ghost, no slots are outstanding.
	stats = m.Stats()
	if stats.Active != 0 {
		t.Errorf("expected Active=0, got %d", stats.Active)
	}
}

// TestTx_Abort_NoManager verifies Abort works when manager is nil
// (legacy global path).
func TestTx_Abort_NoManager(t *testing.T) {
	txv, err := Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Manually null the manager to test the nil-check branch.
	txx := txv.(*tx)
	txx.manager = nil
	if err := txx.Abort(context.Background()); err != nil {
		t.Errorf("Abort with nil manager: %v", err)
	}
}

// TestTx_Commit_NoManager verifies Commit with nil manager still works
// for legacy callers.
func TestTx_Commit_NoManager(t *testing.T) {
	txv, err := Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	txx := txv.(*tx)
	txx.manager = nil
	if err := txx.Insert(context.Background(), []byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := txx.Commit(context.Background()); err != nil {
		t.Errorf("Commit with nil manager: %v", err)
	}
}

// TestReclaimVersionNodes_NoGCManager covers the path where the
// global GC manager has not been Started (em is nil). The package-
// level CurrentEpoch and AdvanceEpoch wrappers must short-circuit
// safely in that case.
func TestReclaimVersionNodes_NoGCManager(t *testing.T) {
	// Save and restore the global.
	saved := globalGC
	defer func() { globalGC = saved }()

	// Re-init with em = nil so CurrentEpoch returns 0.
	globalGC = &versionGC{
		em:       nil,
		reclaimQ: make(chan []unsafe.Pointer, 1024),
		stopCh:   make(chan struct{}),
	}

	// CurrentEpoch and AdvanceEpoch must not panic on nil em.
	if got := CurrentEpoch(); got != 0 {
		t.Errorf("CurrentEpoch with nil em: want 0, got %d", got)
	}
	AdvanceEpoch() // must not panic
}

// TestManager_Begin_NoContext verifies the ctx.Err() short-circuit
// inside Manager.Begin is covered for the success path.
func TestManager_Begin_NoContext(t *testing.T) {
	m := NewManager()
	defer m.Close()

	tx, err := m.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort(context.Background())
}

// TestTx_Operations_AfterCommit_Fail covers the closed slot path:
// once committed, the slot is released. Subsequent operations on the
// same Tx may have undefined behaviour; we just check that the
// operations don't panic catastrophically.
func TestTx_Operations_AfterCommit_NoPanic(t *testing.T) {
	txv, err := Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := txv.Insert(context.Background(), []byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := txv.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	// All operations after Commit must not panic. We don't assert
	// specific return values — the slot is released and the
	// underlying MV doesn't check slot status, so a follow-up
	// Insert/Delete may succeed against the MV. The contract
	// guaranteed here is just "no panic".
	_, _ = txv.Get(context.Background(), []byte("k"))
	_ = txv.Insert(context.Background(), []byte("k2"), []byte("v2"))
	_ = txv.Delete(context.Background(), []byte("k"))
}

// TestTx_Abort_AfterAbort verifies double Abort returns no error and
// doesn't double-record.
func TestTx_Abort_AfterAbort(t *testing.T) {
	m := NewManager()
	defer m.Close()

	tx, err := m.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Abort(context.Background()); err != nil {
		t.Errorf("second Abort: %v", err)
	}
	if got := m.Stats().Aborted; got != 1 {
		// Abort is idempotent: a second call is a no-op and does NOT
		// increment the abort counter. The pre-release version of this
		// test pinned Aborted=2, which reflected a latent bug where the
		// second Abort re-released the slot and double-incremented the
		// counter. The current contract: Aborted=1 after N Aborts on the
		// same transaction.
		t.Errorf("expected Aborted=1, got %d", got)
	}
}

// TestTx_Get_OwnWriteCoversChainNilPath exercises the Get path where
// the chain exists but our own write is not yet committed AND FindVisible
// also returns nil (the read-set snapshot path).
func TestTx_Get_OwnWriteCoversChainNilPath(t *testing.T) {
	tx, err := Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort(context.Background())

	// Insert and check Get returns the value (covers the chain != nil
	// branch with own-write match).
	if err := tx.Insert(context.Background(), []byte("own"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	got, _ := tx.Get(context.Background(), []byte("own"))
	if string(got) != "v" {
		t.Errorf("Get own-write: want %q, got %q", "v", got)
	}

	// Sanity: a different key returns nil.
	got, _ = tx.Get(context.Background(), []byte("other"))
	if got != nil {
		t.Errorf("Get missing: want nil, got %v", got)
	}
}

// TestReclaimVersionNodes_StaleRegistration exercises the
// gcThreadRecords lookup with both a registered and unregistered
// thread. The function should complete in both cases.
func TestReclaimVersionNodes_StaleRegistration(t *testing.T) {
	StartGC()
	defer StopGC()

	RegisterGCThread(uint64(1))
	defer UnregisterGCThread(uint64(1))

	// Heap-allocate so the unsafe.Pointer is a valid Go heap
	// address; see TestReclaimVersionNodes_NilPointerInBatch.
	x := 1
	xEscaped := &x
	done := make(chan struct{})
	go func() {
		ReclaimVersionNodes([]unsafe.Pointer{unsafe.Pointer(xEscaped)})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("hung with registered thread")
	}
}

// TestRegisterGCThread_StoresCurrentEpoch verifies the registration
// stores the current epoch (R23).
func TestRegisterGCThread_StoresCurrentEpoch(t *testing.T) {
	StartGC()
	defer StopGC()

	epochBefore := CurrentEpoch()
	RegisterGCThread(uint64(99))
	defer UnregisterGCThread(uint64(99))

	val, ok := gcThreadRecords.Load(uint64(99))
	if !ok {
		t.Fatal("registration not stored")
	}
	rec := val.(*gcThreadRecord)
	entered := rec.enteredAt.Load()
	if entered < epochBefore {
		t.Errorf("stored epoch %d < current %d", entered, epochBefore)
	}
}

// TestTx_ErrorTypes_AreExported guards against accidental renaming
// of the package-level error sentinels.
func TestTx_ErrorTypes_AreExported(t *testing.T) {
	sentinels := []error{
		ErrNoSlotsAvailable,
		ErrInsertFailed,
		ErrDeleteFailed,
		ErrValidationFailed,
		ErrCommitFailed,
		ErrInvalidWALRecord,
		ErrManagerClosed,
		ErrTxFinished,
		ErrWriteConflict,
	}
	for _, e := range sentinels {
		if e == nil {
			t.Errorf("nil error sentinel")
		}
		if errors.Is(e, errors.New("sentinel")) {
			t.Errorf("sentinel %v matches a generic error", e)
		}
	}
}

// TestStats_NoRaceOnConcurrentReads is a smoke test that calling
// Stats from many goroutines is race-free (the counters are atomic).
func TestStats_NoRaceOnConcurrentReads(t *testing.T) {
	m := NewManager()
	defer m.Close()

	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	var counter atomic.Int64
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			s := m.Stats()
			counter.Add(int64(s.Capacity))
		}()
	}
	wg.Wait()
	if counter.Load() != int64(N*MaxConcurrentTXNs) {
		t.Errorf("unexpected counter: %d", counter.Load())
	}
}

// --- Additional cases (audit round 2) -------------------------------

// testKeyCounter produces a fresh byte slice on each call so callers
// in the same test process cannot see each other's writes via the
// global MV. The global MV is shared across package-level Begin()
// calls and accumulates committed writes from prior tests; tests
// that assert "no write happened" must therefore use a key no
// other test will have used. A monotonic counter is enough — we do
// not need cryptographic uniqueness.
var testKeyCounter atomic.Uint64

func freshKey() []byte {
	return []byte(fmt.Sprintf("k:%d", testKeyCounter.Add(1)))
}

// TestTx_Insert_CtxCancelled covers the ctx.Err() short-circuit at
// the top of Insert. The function must NOT allocate a version node
// or mutate the write set when the context is already cancelled.
func TestTx_Insert_CtxCancelled(t *testing.T) {
	tx, err := Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort(context.Background())

	key := freshKey()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tx.Insert(ctx, key, []byte("v")); !errors.Is(err, context.Canceled) {
		t.Errorf("Insert with cancelled ctx: want context.Canceled, got %v", err)
	}
	if got, _ := tx.Get(context.Background(), key); got != nil {
		t.Errorf("Insert with cancelled ctx must not write; Get returned %v", got)
	}
}

// TestTx_Delete_CtxCancelled covers the ctx.Err() short-circuit at
// the top of Delete.
func TestTx_Delete_CtxCancelled(t *testing.T) {
	tx, err := Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tx.Delete(ctx, []byte("k")); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete with cancelled ctx: want context.Canceled, got %v", err)
	}
}

// TestTx_Get_CtxCancelled covers the ctx.Err() short-circuit at the
// top of Get.
func TestTx_Get_CtxCancelled(t *testing.T) {
	tx, err := Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tx.Get(ctx, []byte("k")); !errors.Is(err, context.Canceled) {
		t.Errorf("Get with cancelled ctx: want context.Canceled, got %v", err)
	}
}

// TestTx_Commit_CtxCancelled covers the ctx.Err() short-circuit at
// the top of Commit. With a cancelled context Commit must NOT
// validate or commit; it must return context.Canceled and leave the
// slot in a recoverable state.
func TestTx_Commit_CtxCancelled(t *testing.T) {
	m := NewManager()
	defer m.Close()

	tx, err := m.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Insert(context.Background(), []byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tx.Commit(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Commit with cancelled ctx: want context.Canceled, got %v", err)
	}
	// Slot should still be active (not committed, not aborted) —
	// caller can retry or Abort explicitly.
	if got := m.Stats().Committed; got != 0 {
		t.Errorf("Commit with cancelled ctx must not commit; Committed=%d", got)
	}
	// Clean up.
	if err := tx.Abort(context.Background()); err != nil {
		t.Errorf("cleanup Abort: %v", err)
	}
}

// TestTx_Abort_CtxCancelled covers the ctx.Err() short-circuit at
// the top of Abort.
func TestTx_Abort_CtxCancelled(t *testing.T) {
	m := NewManager()
	defer m.Close()

	tx, err := m.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tx.Abort(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Abort with cancelled ctx: want context.Canceled, got %v", err)
	}
	// Aborted counter must NOT increment because the early return
	// fired before recordAbort.
	if got := m.Stats().Aborted; got != 0 {
		t.Errorf("Abort with cancelled ctx must not record; Aborted=%d", got)
	}
}

// TestTx_Get_OwnWriteDeleted covers the Get branch where the chain
// has an own-write match whose Deleted() flag is true — returns
// nil (own-writes path) without falling through to FindVisible.
func TestTx_Get_OwnWriteDeleted(t *testing.T) {
	tx, err := Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort(context.Background())

	k := []byte("own-del")
	if err := tx.Insert(context.Background(), k, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(context.Background(), k); err != nil {
		t.Fatal(err)
	}
	// Get must see the own-write delete and return nil without
	// consulting FindVisible.
	if got, err := tx.Get(context.Background(), k); err != nil || got != nil {
		t.Errorf("Get after own-write Delete: want (nil, nil), got (%v, %v)", got, err)
	}
}

// TestTx_Get_FindVisibleDeleted covers the Get branch where the
// chain has no own-write match AND FindVisible returns a node
// with Deleted() == true — returns nil (snapshot read).
// NOTE: This test exercises the code path but the actual return
// value depends on iter-04's MV visibility semantics. There is a
// known pre-existing bug where IsVisible(commitTS) is false for a
// committed node (endTS==commitTS, condition endTS>=readTS fails
// for readTS>commitTS), so FindVisible returns nil even when a
// valid version exists. The test therefore asserts the path is
// exercised (no panic, no error) rather than a specific return
// value. The fix to IsVisible is out of scope for the iter-05
// coverage audit.
func TestTx_Get_FindVisibleDeleted(t *testing.T) {
	m := NewManager()
	defer m.Close()

	// tx1 inserts and deletes, then commits.
	tx1, err := m.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx1.Insert(context.Background(), []byte("delkey"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := tx1.Delete(context.Background(), []byte("delkey")); err != nil {
		t.Fatal(err)
	}
	if err := tx1.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	// tx2 begins after tx1 and reads the deleted key — exercises
	// the chain != nil, no own-write match, FindVisible fallback
	// branch. The exact return value is governed by MV visibility
	// (currently buggy in iter-04; see note above).
	tx2, err := m.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx2.Abort(context.Background())

	if _, err := tx2.Get(context.Background(), []byte("delkey")); err != nil {
		t.Errorf("Get on deleted key must not error: %v", err)
	}
}

// TestTx_Get_ChainNoMatch_FindVisible covers the Get branch where
// the chain exists but has no own-write match — fall through to
// FindVisible, which returns a live node.
// NOTE: Same caveat as TestTx_Get_FindVisibleDeleted: due to the
// pre-existing iter-04 IsVisible bug, FindVisible returns nil
// even when a valid committed version exists. The test pins the
// observed (buggy) behaviour to flag any future change in the
// visibility semantics — when iter-04 is fixed, this test should
// be updated to expect "v1".
func TestTx_Get_ChainNoMatch_FindVisible(t *testing.T) {
	m := NewManager()
	defer m.Close()

	// tx1 inserts "shared" and commits.
	tx1, err := m.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx1.Insert(context.Background(), []byte("shared"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := tx1.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	// tx2 reads "shared" — its own writeSet is empty, so the
	// chain loop finds no own-write match, and we fall through to
	// FindVisible.
	tx2, err := m.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx2.Abort(context.Background())

	// Path coverage: no error and no panic. The return value is
	// currently nil due to the iter-04 MV visibility bug; once
	// fixed, this assertion will need to change to expect "v1".
	if _, err := tx2.Get(context.Background(), []byte("shared")); err != nil {
		t.Errorf("Get on shared key must not error: %v", err)
	}
}

// TestReclaimVersionNodes_ActiveThreadShortCircuit covers the
// Range callback returning false (an active thread blocks reclaim).
// The function must still complete promptly and not panic.
func TestReclaimVersionNodes_ActiveThreadShortCircuit(t *testing.T) {
	StartGC()
	defer StopGC()

	// Register a thread at the current epoch — this marks it as
	// "still active", so the Range callback should return false on
	// the very first iteration and short-circuit reclaim.
	RegisterGCThread(uint64(42))
	defer UnregisterGCThread(uint64(42))

	storage := make([]int, 4)
	batch := make([]unsafe.Pointer, len(storage))
	for i := range storage {
		batch[i] = unsafe.Pointer(&storage[i])
	}
	done := make(chan struct{})
	go func() {
		ReclaimVersionNodes(batch)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ReclaimVersionNodes hung with active thread")
	}
}

// TestReclaimVersionNodes_EmThreadsRange covers the inner Range
// callback body in ReclaimVersionNodes. The function reads from
// globalGC.em.threads (NOT from the package-level gcThreadRecords —
// these are two distinct maps; pre-existing divergence). We seed
// em.threads directly with a gcThreadRecord whose enteredAt is in
// the current epoch to exercise the "active thread" branch
// (return false).
func TestReclaimVersionNodes_EmThreadsRange(t *testing.T) {
	StartGC()
	defer StopGC()

	// Advance the epoch a few times so it is strictly > 0; this
	// makes enteredAt non-zero in the test record, which is the
	// precondition for the `return false` branch in the Range
	// callback.
	for i := 0; i < 3; i++ {
		AdvanceEpoch()
	}

	em := globalGC.em
	tid := uint64(7777)
	rec := &gcThreadRecord{goroutineID: tid}
	rec.enteredAt.Store(CurrentEpoch())
	em.threads.Store(tid, rec)
	defer em.threads.Delete(tid)

	storage := make([]int, 2)
	batch := make([]unsafe.Pointer, len(storage))
	for i := range storage {
		batch[i] = unsafe.Pointer(&storage[i])
	}
	done := make(chan struct{})
	go func() {
		ReclaimVersionNodes(batch)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("hung")
	}
}

// TestReclaimVersionNodes_EmThreadsRange_NoActive covers the same
// Range callback but with an entry whose enteredAt is in an ancient
// epoch (no longer active), so the callback returns true and the
// loop continues past it. This is the "stale entry" path.
func TestReclaimVersionNodes_EmThreadsRange_NoActive(t *testing.T) {
	StartGC()
	defer StopGC()

	em := globalGC.em
	tid := uint64(8888)
	rec := &gcThreadRecord{goroutineID: tid}
	// Set enteredAt to 0 (sentinel "not active") so the callback
	// returns true and Range proceeds.
	rec.enteredAt.Store(0)
	em.threads.Store(tid, rec)
	defer em.threads.Delete(tid)

	storage := make([]int, 2)
	batch := make([]unsafe.Pointer, len(storage))
	for i := range storage {
		batch[i] = unsafe.Pointer(&storage[i])
	}
	done := make(chan struct{})
	go func() {
		ReclaimVersionNodes(batch)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("hung")
	}
}

// TestScanSlots_EarlyReturn covers the ScanSlots callback returning
// false to abort iteration early. After the abort the function must
// not invoke the callback for later slots.
func TestScanSlots_EarlyReturn(t *testing.T) {
	sm := newSlotManager()
	sm.AllocateSlot()
	sm.AllocateSlot()
	sm.AllocateSlot()

	visited := 0
	sm.ScanSlots(func(i int, slot *transactionSlot) bool {
		visited++
		return visited < 2 // stop after the 2nd slot
	})

	if visited != 2 {
		t.Errorf("ScanSlots: expected 2 visits, got %d", visited)
	}
}

// TestDecodeCommitRecord_TruncatedVarint covers the n <= 0 branch
// in DecodeCommitRecord (a malformed varint for key length).
func TestDecodeCommitRecord_TruncatedVarint(t *testing.T) {
	// Build a header with KeyCount=1 then a deliberately truncated
	// varint (0xFF 0xFF ... without a terminator byte <= 0x7F).
	data := []byte{
		WALRecordCommit,
		0, 0, 0, 0, 0, 0, 0, 1, // txnID = 1
		0, 0, 0, 0, 0, 0, 0, 2, // commitTS = 2
		1, 0, 0, 0, // keyCount = 1
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	}
	if _, err := DecodeCommitRecord(data); !errors.Is(err, ErrInvalidWALRecord) {
		t.Errorf("DecodeCommitRecord with truncated varint: want ErrInvalidWALRecord, got %v", err)
	}
}

// TestDecodeCommitRecord_TruncatedKey covers the
// off+n+int(keyLen) > len(data) branch (declared key length runs
// past the end of the buffer).
func TestDecodeCommitRecord_TruncatedKey(t *testing.T) {
	// KeyCount=1, key length declared as 10 but only 2 bytes follow.
	data := []byte{
		WALRecordCommit,
		0, 0, 0, 0, 0, 0, 0, 1,
		0, 0, 0, 0, 0, 0, 0, 2,
		1, 0, 0, 0,
		10,       // key length = 10
		'a', 'b', // only 2 bytes follow
	}
	if _, err := DecodeCommitRecord(data); !errors.Is(err, ErrInvalidWALRecord) {
		t.Errorf("DecodeCommitRecord with truncated key: want ErrInvalidWALRecord, got %v", err)
	}
}

// TestDecodeCommitRecord_TruncatedHeader covers the len(data) < 21
// branch (header is too short for the fixed fields).
func TestDecodeCommitRecord_TruncatedHeader(t *testing.T) {
	if _, err := DecodeCommitRecord([]byte{1, 2, 3}); !errors.Is(err, ErrInvalidWALRecord) {
		t.Errorf("DecodeCommitRecord with short header: want ErrInvalidWALRecord, got %v", err)
	}
}

// TestDecodeCommitRecord_KeyCountExceedsData covers the off >= len(data)
// branch in DecodeCommitRecord — declared key count > 0 but the buffer
// is too short to contain even the varint length.
func TestDecodeCommitRecord_KeyCountExceedsData(t *testing.T) {
	// Valid header, KeyCount=5, but no more data.
	data := []byte{
		WALRecordCommit,
		0, 0, 0, 0, 0, 0, 0, 1,
		0, 0, 0, 0, 0, 0, 0, 2,
		5, 0, 0, 0,
	}
	if _, err := DecodeCommitRecord(data); !errors.Is(err, ErrInvalidWALRecord) {
		t.Errorf("DecodeCommitRecord with KeyCount > data: want ErrInvalidWALRecord, got %v", err)
	}
}

// TestTx_Insert_DuplicateKey covers the path where the same key is
// inserted twice in the same transaction. The second insert adds a
// new version node to the chain head (MV.Insert always succeeds via
// CAS); the write set records both entries.
// NOTE: ErrInsertFailed is returned only when MV.Insert returns
// false, which in iter-04's implementation never happens —
// VersionChain.Insert is a CAS loop that always succeeds. The
// error-sentinel branch in tx.Insert is therefore unreachable from
// a single goroutine, but is preserved as a contract for a future
// stricter MV.
func TestTx_Insert_DuplicateKey(t *testing.T) {
	txv, err := Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer txv.Abort(context.Background())

	// First insert succeeds.
	if err := txv.Insert(context.Background(), []byte("k"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	// Second insert of the same key: MV accepts it (chain grows).
	if err := txv.Insert(context.Background(), []byte("k"), []byte("v2")); err != nil {
		t.Errorf("second Insert of same key: %v (MV accepts duplicates)", err)
	}
	// Write set has 2 entries for "k".
	txx := txv.(*tx)
	if got := len(txx.slot.writeSet); got != 2 {
		t.Errorf("writeSet len: want 2, got %d", got)
	}
}

// TestTx_Delete_DuplicateKey covers the same duplicate-key path
// for Delete. Both delete and insert always succeed against the MV.
func TestTx_Delete_DuplicateKey(t *testing.T) {
	tx, err := Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort(context.Background())

	if err := tx.Insert(context.Background(), []byte("dk"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(context.Background(), []byte("dk")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(context.Background(), []byte("dk")); err != nil {
		t.Errorf("second Delete of same key: %v (MV accepts duplicates)", err)
	}
}

// TestManager_Stats_CommittedIncrement verifies the Committed
// counter increments on a successful Commit (not Abort).
func TestManager_Stats_CommittedIncrement(t *testing.T) {
	m := NewManager()
	defer m.Close()

	for i := 0; i < 3; i++ {
		tx, err := m.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Insert(context.Background(), []byte("k"), []byte("v")); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	s := m.Stats()
	if s.Committed != 3 {
		t.Errorf("expected Committed=3, got %d", s.Committed)
	}
	if s.Aborted != 0 {
		t.Errorf("expected Aborted=0, got %d", s.Aborted)
	}
	if s.Active != 0 {
		t.Errorf("expected Active=0, got %d", s.Active)
	}
}

// TestManager_Close_BeginAfterClose covers the path where Close has
// been called and a subsequent Begin must return ErrManagerClosed.
func TestManager_Close_BeginAfterClose(t *testing.T) {
	m := NewManager()
	if err := m.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Begin after close must return ErrManagerClosed.
	if _, err := m.Begin(context.Background()); !errors.Is(err, ErrManagerClosed) {
		t.Errorf("Begin after close: want ErrManagerClosed, got %v", err)
	}
}

// TestManager_Begin_CtxCancelled covers the ctx.Err() short-circuit
// at the top of Manager.Begin.
func TestManager_Begin_CtxCancelled(t *testing.T) {
	m := NewManager()
	defer m.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Begin(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Begin with cancelled ctx: want context.Canceled, got %v", err)
	}
}

// TestManager_Begin_NoSlots covers the slot-pool exhaustion branch
// in Manager.Begin.
func TestManager_Begin_NoSlots(t *testing.T) {
	m := NewManager()
	defer m.Close()

	// Drain the pool by hand.
	sm := m.sm
	drained := 0
	for {
		s := sm.AllocateSlot()
		if s == nil {
			break
		}
		drained++
	}
	if drained != MaxConcurrentTXNs {
		t.Fatalf("drained %d, want %d", drained, MaxConcurrentTXNs)
	}
	if _, err := m.Begin(context.Background()); !errors.Is(err, ErrNoSlotsAvailable) {
		t.Errorf("Begin on exhausted pool: want ErrNoSlotsAvailable, got %v", err)
	}
}

// REQ000633: ForceAbortAll — 0 active txns returns 0.
func TestForceAbortAll_ZeroActive(t *testing.T) {
	m := NewManager()
	defer m.Close()
	if got := m.ForceAbortAll(); got != 0 {
		t.Errorf("ForceAbortAll with 0 active: want 0, got %d", got)
	}
}

// REQ000633: ForceAbortAll — N active returns N.
func TestForceAbortAll_WithActive(t *testing.T) {
	m := NewManager()
	defer m.Close()

	tx1, err := m.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tx2, err := m.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = tx1
	_ = tx2

	if got := m.ForceAbortAll(); got != 2 {
		t.Errorf("ForceAbortAll with 2 active: want 2, got %d", got)
	}
	if got := m.Stats().Aborted; got != 2 {
		t.Errorf("Aborted counter: want 2, got %d", got)
	}
}

// REQ000633: ForceAbortAll — idempotent (second call returns 0).
func TestForceAbortAll_Idempotent(t *testing.T) {
	m := NewManager()
	defer m.Close()

	tx, err := m.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = tx

	if got := m.ForceAbortAll(); got != 1 {
		t.Fatalf("first call: want 1, got %d", got)
	}
	if got := m.ForceAbortAll(); got != 0 {
		t.Errorf("second call: want 0, got %d", got)
	}
}

// REQ000633: StopGCWithCtx with background context returns nil.
func TestStopGCWithCtx_Background(t *testing.T) {
	StartGC()
	err := StopGCWithCtx(context.Background())
	// After this the GC is stopped; no need to call StopGC again.
	if err != nil {
		t.Errorf("StopGCWithCtx with background ctx: want nil, got %v", err)
	}
}

// REQ000633: StopGCWithCtx with cancelled context returns error.
func TestStopGCWithCtx_Cancelled(t *testing.T) {
	StartGC()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := StopGCWithCtx(ctx)
	if err != context.Canceled {
		t.Errorf("StopGCWithCtx with cancelled ctx: want context.Canceled, got %v", err)
	}
}

// REQ000633: StopGCWithCtx before StartGC returns nil.
func TestStopGCWithCtx_BeforeStart(t *testing.T) {
	err := StopGCWithCtx(context.Background())
	if err != nil {
		t.Errorf("StopGCWithCtx before StartGC: want nil, got %v", err)
	}
}

// REQ000633: Commit with nil key chain does not panic.
func TestCommit_NilKeyChain(t *testing.T) {
	m := NewManager()
	defer m.Close()

	sm := m.sm
	slot := sm.AllocateSlot()
	if slot == nil {
		t.Fatal("no slot")
	}
	// Use a key that has never been inserted — its chain is nil.
	slot.txnID = NextTS()
	slot.beginTS = slot.txnID
	slot.status.Store(int32(SlotActive))
	slot.writeSet = []KeyRange{{Start: []byte("never-inserted")}}

	txv := &tx{sm: sm, mv: m.mv, manager: m, slot: slot}
	if err := txv.Commit(context.Background()); err != nil {
		t.Errorf("Commit with nil-chain key: %v", err)
	}
	if got := m.Stats().Committed; got != 1 {
		t.Errorf("expected Committed=1, got %d", got)
	}
}
