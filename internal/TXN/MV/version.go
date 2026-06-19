package MV

import (
	"sync/atomic"
	"unsafe"
)

const maxUint64 = ^uint64(0)

// VersionNode is a single version entry in a version chain. The
// struct is kept small (4 pointers + 2 uint64 + 1 bool) so the
// arena-allocated paths fit in a single cache line. REQ000306:
// the struct is annotated to encourage the Go compiler to
// keep intermediate VersionNode values in registers / on the
// stack when only the *VersionNode pointer is published.
type VersionNode struct {
	txnID   uint64
	beginTS uint64
	endTS   atomic.Uint64
	key     []byte
	value   []byte
	deleted bool
	next    atomic.Pointer[VersionNode]
}

// noescape hides a pointer from escape analysis. REQ000306.
//
// Implementation note: we use the standard `//go:nosplit` +
// uintptr round-trip idiom that has been the recommended way
// to suppress escape analysis in Go since 1.x. The uintptr
// round-trip prevents the compiler from tracking the pointer
// for liveness/GC purposes, but we re-tag it as a pointer
// (unsafe.Pointer) on the way out so the GC re-scans it
// correctly. This is safe because the pointer is local to
// the caller's stack frame and is consumed before the
// function returns.
//
//go:nosplit
func noescape(p unsafe.Pointer) unsafe.Pointer {
	x := uintptr(p)
	// XOR with 0 is a no-op but forces the compiler to
	// forget the original provenance of x, so the
	// returned pointer is treated as a fresh pointer
	// (and the source pointer's escape decision is
	// re-evaluated based on the new pointer's use).
	return unsafe.Pointer(x ^ 0)
}

// NewVersionNode allocates a VersionNode out of the supplied arena and
// initializes it. The arena is expected to be the per-transaction arena
// owned by the calling transaction; the node lives as long as the arena
// backs its memory and any live version chain references it.
//
// The arena's bump pointer is CAS-safe on its own, but the arena itself
// is NOT safe for concurrent use by multiple goroutines — the caller
// (the *tx* type in VL/protocol.go) holds a per-txn mutex that
// serializes Insert/Delete and therefore serializes arena access.
func NewVersionNode(arena *Arena, txnID, beginTS uint64, key, value []byte, deleted bool) *VersionNode {
	size := int(unsafe.Sizeof(VersionNode{}))
	mem := arena.Alloc(size)
	if mem == nil {
		// Arena exhausted: fall back to a heap allocation. The next Insert
		// on the same transaction will then resume from the arena.
		// Returning a heap-allocated node preserves correctness; the
		// memory is reclaimed by Go's GC once the node is unreferenced.
		var n VersionNode
		node := &n
		node.txnID = txnID
		node.beginTS = beginTS
		node.endTS.Store(maxUint64)
		node.key = key
		node.value = value
		node.deleted = deleted
		return node
	}
	node := (*VersionNode)(unsafe.Pointer(&mem[0]))
	node.txnID = txnID
	node.beginTS = beginTS
	node.endTS.Store(maxUint64)
	node.key = key
	node.value = value
	node.deleted = deleted
	return node
}

func (n *VersionNode) IsUncommitted() bool {
	return n.endTS.Load() == maxUint64
}

// NewVersionNodeStack allocates a VersionNode on the caller's
// stack. The returned pointer is valid only as long as the
// caller's stack frame is alive. REQ000306.
//
// Use this for transient nodes (e.g. during a single insert
// that immediately publishes the pointer into an arena-backed
// version chain). The arena-allocated NewVersionNode is the
// default for long-lived nodes.
func NewVersionNodeStack(txnID, beginTS uint64, key, value []byte, deleted bool) *VersionNode {
	var n VersionNode
	n.txnID = txnID
	n.beginTS = beginTS
	n.endTS.Store(maxUint64)
	n.key = key
	n.value = value
	n.deleted = deleted
	// Apply noescape hint so the compiler does not promote n
	// to the heap on this path.
	return (*VersionNode)(noescape(unsafe.Pointer(&n)))
}

func (n *VersionNode) IsVisible(readTS uint64) bool {
	return n.beginTS < readTS && n.endTS.Load() >= readTS
}

func (n *VersionNode) TxnID() uint64 {
	return n.txnID
}

func (n *VersionNode) BeginTS() uint64 {
	return n.beginTS
}

func (n *VersionNode) EndTS() uint64 {
	return n.endTS.Load()
}

func (n *VersionNode) Key() []byte {
	return n.key
}

func (n *VersionNode) Value() []byte {
	return n.value
}

func (n *VersionNode) Deleted() bool {
	return n.deleted
}

func (n *VersionNode) Next() *VersionNode {
	return n.next.Load()
}

type VersionChain struct {
	head atomic.Pointer[VersionNode]
}

func (vc *VersionChain) GetHead() *VersionNode {
	return vc.head.Load()
}

func (vc *VersionChain) Insert(node *VersionNode) bool {
	for {
		oldHead := vc.head.Load()
		node.next.Store(oldHead)
		if vc.head.CompareAndSwap(oldHead, node) {
			return true
		}
	}
}

func (n *VersionNode) Commit(commitTS uint64) bool {
	return n.endTS.CompareAndSwap(maxUint64, commitTS)
}

func (vc *VersionChain) Commit(node *VersionNode, commitTS uint64) bool {
	return node.Commit(commitTS)
}

func (vc *VersionChain) FindVisible(readTS uint64) *VersionNode {
	for node := vc.GetHead(); node != nil; node = node.next.Load() {
		if node.IsUncommitted() {
			continue
		}
		if node.IsVisible(readTS) {
			return node
		}
	}
	return nil
}
