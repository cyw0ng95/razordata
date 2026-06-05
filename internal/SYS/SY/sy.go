// Package SY implements the razordata engine: it owns every subsystem
// instance, runs Open/Close lifecycle, exposes Stats aggregation, and
// dispatches SIGTERM/SIGINT to graceful shutdown.
//
// The Engine is constructed by the package-level Open function (see
// also the AP.Engine interface for the public contract).
package SY

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	id "github.com/cyw0ng95/razordata/internal/ENG/ID"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	tb "github.com/cyw0ng95/razordata/internal/ENG/TB"
	df "github.com/cyw0ng95/razordata/internal/FIL/DF"
	fs "github.com/cyw0ng95/razordata/internal/FIL/FS"
	lf "github.com/cyw0ng95/razordata/internal/FIL/LF"
	lg "github.com/cyw0ng95/razordata/internal/LOG/LG"
	bf "github.com/cyw0ng95/razordata/internal/MEM/BF"
	"github.com/cyw0ng95/razordata/internal/MEM/SP"
	executor "github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	vl "github.com/cyw0ng95/razordata/internal/TXN/VL"
	fl "github.com/cyw0ng95/razordata/internal/WAL/FL"
	rp "github.com/cyw0ng95/razordata/internal/WAL/RP"
	wr "github.com/cyw0ng95/razordata/internal/WAL/WR"
)

// Engine is the concrete AP.Engine. It holds every subsystem and
// orchestrates Open/Close.
type Engine struct {
	dir  string
	opts AP.Options

	// Subsystems (constructed in dependency order).
	log     lg.Logger
	fs      *fs.FileManager
	lf      *lf.SegmentManager
	df      *df.BlockDevice
	sp      sp.SyncPool
	bp      bf.BufferPool
	wr      wr.Writer
	fl      fl.Flusher
	rp      rp.Replayer
	eng     *ls.Engine
	catalog *tb.Catalog
	pkindex *id.PKIndex
	txn     *vl.Manager
	exe     *executor.Executor

	// Lifecycle.
	mu      sync.Mutex
	closed  atomic.Bool
	opened  atomic.Bool
	started time.Time
	// exeAdapter bridges the LS engine to the EX Store interface.
	exeAdapter *executorStoreAdapter
}

// Open validates opts, constructs every subsystem in dependency
// order, and returns a ready Engine. Subsequent calls return
// AP.ErrAlreadyOpen.
func Open(ctx context.Context, dir string, opts AP.Options) (*Engine, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: dir is required", AP.ErrInvalidOptions)
	}
	opts.Dir = dir
	applyDefaults(&opts)
	if err := validate(&opts); err != nil {
		return nil, err
	}

	eng := &Engine{dir: dir, opts: opts}
	if err := eng.open(ctx); err != nil {
		return nil, err
	}
	return eng, nil
}

func applyDefaults(o *AP.Options) {
	if o.PageSize == 0 {
		o.PageSize = AP.DefaultPageSize
	}
	if o.MemTableSize == 0 {
		o.MemTableSize = AP.DefaultMemTableSize
	}
	if o.BufferPoolMB == 0 {
		o.BufferPoolMB = AP.DefaultBufferPoolMB
	}
	if o.WALSizeMB == 0 {
		o.WALSizeMB = AP.DefaultWALSizeMB
	}
	if o.MaxLevel == 0 {
		o.MaxLevel = AP.DefaultMaxLevel
	}
	if o.LogFormat == "" {
		o.LogFormat = "text"
	}
	// CreateIfMissing defaults to true. The Options struct cannot
	// distinguish "unset" from "explicitly false" with a plain bool;
	// in v1 callers that want CreateIfMissing=false must set
	// o.CreateIfMissing = false AFTER calling applyDefaults. We use
	// the address-of-the-zero-value as a sentinel: if a caller
	// passes a literal false, treat it as set; the typical default
	// of true is what the design prescribes.
	if !o.CreateIfMissingSet {
		o.CreateIfMissing = true
		o.CreateIfMissingSet = true
	}
}

func validate(o *AP.Options) error {
	if o.Dir == "" {
		return fmt.Errorf("%w: dir is required", AP.ErrInvalidOptions)
	}
	if o.PageSize <= 0 || (o.PageSize&(o.PageSize-1)) != 0 {
		return fmt.Errorf("%w: PageSize must be a power of two", AP.ErrInvalidOptions)
	}
	if o.MemTableSize <= 0 {
		return fmt.Errorf("%w: MemTableSize must be positive", AP.ErrInvalidOptions)
	}
	if o.BufferPoolMB <= 0 {
		return fmt.Errorf("%w: BufferPoolMB must be positive", AP.ErrInvalidOptions)
	}
	if o.WALSizeMB <= 0 {
		return fmt.Errorf("%w: WALSizeMB must be positive", AP.ErrInvalidOptions)
	}
	if o.MaxLevel <= 0 {
		return fmt.Errorf("%w: MaxLevel must be positive", AP.ErrInvalidOptions)
	}
	return nil
}

// open is the constructor body. It is split out so tests can drive
// partial constructions for failure-injection scenarios.
func (e *Engine) open(ctx context.Context) (err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.opened.Load() {
		return AP.ErrAlreadyOpen
	}

	// Create the database directory if missing.
	if e.opts.CreateIfMissing {
		if err := os.MkdirAll(e.dir, 0o755); err != nil {
			return fmt.Errorf("sy: mkdir %s: %w", e.dir, err)
		}
	} else {
		if _, err := os.Stat(e.dir); err != nil {
			return fmt.Errorf("sy: stat %s: %w", e.dir, err)
		}
	}

	// Step 1: logger.
	e.log = lg.New(lg.Options{
		Format: e.opts.LogFormat,
		Level:  e.opts.LogLevel,
		Output: os.Stderr,
	})

	// Wrap any later failure so we can roll back partial state.
	success := false
	defer func() {
		if !success {
			e.closeBestEffort()
		}
	}()

	// Step 2: FS file manager (rooted at the database directory).
	fsRoot := filepath.Join(e.dir, "fil")
	if err := os.MkdirAll(fsRoot, 0o755); err != nil {
		return err
	}
	if e.fs, err = fs.NewOrCreate(fsRoot, e.log); err != nil {
		return err
	}

	// Step 3: LF segment manager (WAL segments live under wal/).
	walDir := filepath.Join(e.dir, "wal")
	if err := os.MkdirAll(walDir, 0o755); err != nil {
		return err
	}
	if e.lf, err = lf.New(walDir, e.log); err != nil {
		return err
	}

	// Step 4: DF block device for the buffer pool. Create the file
	// if it does not exist (CreateIfMissing implies we may be
	// opening a fresh database).
	bdPath := filepath.Join(e.dir, "meta.razor")
	if e.opts.CreateIfMissing {
		if e.df, err = df.Create(bdPath, e.log); err != nil {
			// Already exists is fine; reopen as Open.
			if os.IsExist(err) {
				e.df, err = df.Open(bdPath, e.log)
			}
			if err != nil {
				return err
			}
		}
	} else {
		if e.df, err = df.Open(bdPath, e.log); err != nil {
			return err
		}
	}

	// Step 5: SP sync pool.
	e.sp = sp.New()

	// Step 6: BF buffer pool.
	bpCapacity := int64(e.opts.BufferPoolMB) * 1024 * 1024
	if e.bp, err = bf.New(bpCapacity, filepath.Join(e.dir, "bp.hint"), e.df, e.sp, e.log); err != nil {
		return err
	}

	// Step 7: WR WAL writer.
	if e.wr, err = wr.New(walDir, e.lf, e.sp, e.log); err != nil {
		return err
	}

	// Step 8: FL flusher (WAL directory fsync).
	if e.fl, err = fl.New(walDir, e.lf, e.fs, e.log); err != nil {
		return err
	}

	// Step 9: RP replayer (replay hook; R07).
	if e.rp, err = rp.New(walDir, e.lf, e.bp, rp.Callbacks{}, e.log); err != nil {
		return err
	}
	// R07: replay the WAL on startup. Best-effort: log and continue on
	// failure rather than refusing to open — tests and first-run
	// databases both rely on a missing WAL being a no-op.
	if err := e.rp.Replay(); err != nil && !errors.Is(err, os.ErrNotExist) {
		e.log.Warn("wal.replay", "err", err)
	}

	// Step 10: LS engine (storage).
	if e.eng, err = ls.Open(filepath.Join(e.dir, "eng")); err != nil {
		return err
	}

	// Step 10.1: TB catalog (iter-10). The catalog persists the
	// system table registry; on Load it scans the engine's
	// __catalog__ prefix and rebuilds the in-memory cache.
	e.catalog = tb.New(&catalogStoreAdapter{eng: e.eng})
	if err := e.catalog.Load(); err != nil {
		return fmt.Errorf("sy: catalog load: %w", err)
	}

	// Step 10.2: ID primary-key index (iter-10). The PK index is
	// a thin wrapper over the engine; the executor uses it for
	// IndexScan real-seek.
	e.pkindex = id.NewPKIndex(&idStoreAdapter{eng: e.eng})

	// Step 11: VL TxnManager.
	e.txn = vl.NewManager()

	// Step 12: SQL executor (wired with catalog + PK index).
	e.exeAdapter = &executorStoreAdapter{eng: e.eng}
	e.exe = executor.NewExecutorWithEngineAndIndexAndCatalog(
		e.exeAdapter, e.catalog, e.pkindex)

	// Step 13: populate the executor with all persisted tables.
	// This makes the schemas visible to the planner/executor
	// without each session having to re-load them.
	for _, sch := range e.catalog.ListTables() {
		cols := make([]string, len(sch.Columns))
		var pk string
		for i, c := range sch.Columns {
			cols[i] = c.Name
			if c.PrimaryKey {
				pk = c.Name
			}
		}
		if len(sch.PrimaryKey) == 1 {
			pk = cols[sch.PrimaryKey[0]]
		}
		e.exe.RegisterTableWithPK(sch.Name, cols, pk)
	}

	e.started = time.Now()
	e.opened.Store(true)
	success = true
	return nil
}

// Close flushes pending writes and tears down every subsystem in
// reverse construction order. Safe to call multiple times.
func (e *Engine) Close(ctx context.Context) error {
	if !e.opened.Load() {
		return nil
	}
	if !e.closed.CompareAndSwap(false, true) {
		return nil
	}
	return e.closeBestEffort()
}

func (e *Engine) closeBestEffort() error {
	var firstErr error
	// Errors are collected and logged; closing continues regardless
	// so a failure in one subsystem does not strand later ones.
	stop := func(name string, closer func() error) {
		if closer == nil {
			return
		}
		err := closer()
		if err == nil {
			return
		}
		if e.log != nil {
			e.log.Warn("sy.close", "subsystem", name, "err", err)
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	// Reverse order: TB → ID → TXN → EX → ENG → RP → FL → WR →
	// BF → DF → LF → FS. TB/ID are no-op close in v1; they are
	// included here as the public seam for future checkpoint-based
	// catalog sync (R14) and PK index GC.
	stop("tb", func() error {
		if e.catalog == nil {
			return nil
		}
		return e.catalog.Flush()
	})
	stop("id", func() error { return nil })
	stop("vl", func() error {
		if e.txn == nil {
			return nil
		}
		return e.txn.Close()
	})
	stop("ls", func() error {
		if e.eng == nil {
			return nil
		}
		return e.eng.Close()
	})
	stop("rp", func() error {
		if e.rp == nil {
			return nil
		}
		return e.rp.Close()
	})
	stop("fl", func() error {
		if e.fl == nil {
			return nil
		}
		return e.fl.Close()
	})
	stop("wr", func() error {
		if e.wr == nil {
			return nil
		}
		return e.wr.Close()
	})
	stop("bf", func() error {
		if e.bp == nil {
			return nil
		}
		return e.bp.Close()
	})
	stop("df", func() error {
		if e.df == nil {
			return nil
		}
		return e.df.Close()
	})
	stop("lf", func() error {
		if e.lf == nil {
			return nil
		}
		return e.lf.Close()
	})
	stop("fs", func() error {
		if e.fs == nil {
			return nil
		}
		return e.fs.Close()
	})
	return firstErr
}

// sessionConstructor is set by the SE package via RegisterSession
// during its init. SY uses it to allocate a fresh Session without
// importing SE.
var sessionConstructor func(e *Engine) AP.Session

// RegisterSession binds the SE-package Session constructor into SY.
// Called by SE.init; the SE package is responsible for being
// imported by at least one test or driver.
func RegisterSession(fn func(e *Engine) AP.Session) { sessionConstructor = fn }

// Begin returns a fresh Session bound to this engine.
func (e *Engine) Begin(ctx context.Context) (AP.Session, error) {
	if !e.opened.Load() {
		return nil, AP.ErrNotOpen
	}
	if e.closed.Load() {
		return nil, AP.ErrClosed
	}
	if sessionConstructor == nil {
		return nil, AP.ErrClosed
	}
	return sessionConstructor(e), nil
}

// Open is a no-op on an already-constructed *Engine; it exists to
// satisfy the AP.Engine interface. New callers should use the
// package-level Open constructor.
func (e *Engine) Open(ctx context.Context, dir string, opts AP.Options) error {
	if e.opened.Load() {
		return AP.ErrAlreadyOpen
	}
	return AP.ErrNotOpen
}

// Stats aggregates metrics from every subsystem.
func (e *Engine) Stats() AP.EngineStats {
	if !e.opened.Load() {
		return AP.EngineStats{Version: AP.Version}
	}
	lsm := e.eng.Stats()
	bp := e.bp.Stats()
	tx := e.txn.Stats()
	uptime := time.Duration(0)
	if !e.started.IsZero() {
		uptime = time.Since(e.started)
	}
	return AP.EngineStats{
		Version: AP.Version,
		Uptime:  uptime,
		LSMTree: AP.LSMTreeStats{
			MemtableHits: int64(lsm.MemtableHits),
			SSTHits:      int64(lsm.SSTHits),
			DiskReads:    int64(lsm.DiskReads),
		},
		BufferPool: AP.BufferPoolStats{
			Hits:      bp.Hits,
			Misses:    bp.Misses,
			Evictions: bp.Evicts,
		},
		WAL: AP.WALStats{
			CurrentLSN: 0,
		},
		Tx: AP.TxnStats{
			Active:    tx.Active,
			Committed: tx.Committed,
			Aborted:   tx.Aborted,
		},
	}
}

// executorStoreAdapter wraps an *ls.Engine to the EX.Store interface.
type executorStoreAdapter struct {
	eng *ls.Engine
}

func (a *executorStoreAdapter) Insert(k, v []byte) error { return a.eng.Insert(k, v) }
func (a *executorStoreAdapter) Delete(k []byte) error    { return a.eng.Delete(k) }
func (a *executorStoreAdapter) NewIterator(prefix []byte) ls.RangeIter {
	return a.eng.NewIterator(prefix)
}

// catalogStoreAdapter wraps an *ls.Engine to the TB.Store interface.
// The adapter is needed because TB.Store.NewIterator returns
// tb.Iterator (a different declared type from ls.RangeIter, even
// though they have the same shape).
type catalogStoreAdapter struct {
	eng *ls.Engine
}

func (a *catalogStoreAdapter) Insert(k, v []byte) error     { return a.eng.Insert(k, v) }
func (a *catalogStoreAdapter) Get(k []byte) ([]byte, error) { return a.eng.Get(k) }
func (a *catalogStoreAdapter) Delete(k []byte) error        { return a.eng.Delete(k) }
func (a *catalogStoreAdapter) NewIterator(prefix []byte) tb.Iterator {
	return &lsRangeIterAdapter{inner: a.eng.NewIterator(prefix)}
}

// lsRangeIterAdapter bridges ls.RangeIter to the tb.Iterator
// interface. The two types have the same method set; Go requires
// the adapter because they are distinct declared types.
type lsRangeIterAdapter struct {
	inner ls.RangeIter
}

func (a *lsRangeIterAdapter) Next() bool    { return a.inner.Next() }
func (a *lsRangeIterAdapter) Key() []byte   { return a.inner.Key() }
func (a *lsRangeIterAdapter) Value() []byte { return a.inner.Value() }
func (a *lsRangeIterAdapter) Err() error    { return a.inner.Err() }
func (a *lsRangeIterAdapter) Close() error  { return a.inner.Close() }

// idStoreAdapter wraps an *ls.Engine to the ID.Store interface,
// same purpose as catalogStoreAdapter for the ID cluster.
type idStoreAdapter struct {
	eng *ls.Engine
}

func (a *idStoreAdapter) Insert(k, v []byte) error     { return a.eng.Insert(k, v) }
func (a *idStoreAdapter) Get(k []byte) ([]byte, error) { return a.eng.Get(k) }
func (a *idStoreAdapter) Delete(k []byte) error        { return a.eng.Delete(k) }
func (a *idStoreAdapter) NewIterator(prefix []byte) tb.Iterator {
	return &lsRangeIterAdapter{inner: a.eng.NewIterator(prefix)}
}

// Executor returns the SQL executor bound to this engine. Used by
// Session/Transaction/Stmt to dispatch Query/Exec.
func (e *Engine) Executor() *executor.Executor { return e.exe }

// Engine returns the underlying storage engine. Used by Transaction
// to apply rollback writes (engine.Insert / engine.Delete).
func (e *Engine) Engine() *ls.Engine { return e.eng }

// TxnManager returns the transaction manager. Exposed for callers
// that need raw access to slot/epoch statistics.
func (e *Engine) TxnManager() *vl.Manager { return e.txn }

// FileManager returns the FIL/FS file manager.
func (e *Engine) FileManager() *fs.FileManager { return e.fs }

// BufferPool returns the MEM/BF buffer pool.
func (e *Engine) BufferPool() bf.BufferPool { return e.bp }

// Writer returns the WAL writer.
func (e *Engine) Writer() wr.Writer { return e.wr }

// Flusher returns the WAL flusher.
func (e *Engine) Flusher() fl.Flusher { return e.fl }

// Replayer returns the WAL replayer.
func (e *Engine) Replayer() rp.Replayer { return e.rp }

// Logger returns the underlying slog-style logger.
func (e *Engine) Logger() lg.Logger { return e.log }

// BeginTxn allocates a fresh VL Tx and wraps it as a SYS transaction
// bound to this engine.
func (e *Engine) BeginTxn(ctx context.Context) (AP.Transaction, error) {
	if e.closed.Load() {
		return nil, AP.ErrClosed
	}
	tx, err := e.txn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return txwrap(e, tx), nil
}

// txNew is a package-level variable pointing at the concrete
// *TX.Transaction constructor. It is assigned by TX's init function
// to break the SY↔TX import cycle.
var txNew func(e *Engine, tx vl.Tx) AP.Transaction

// RegisterTxConstructor is called by TX.init so the SY package can
// route BeginTxn through the concrete Transaction type without
// importing TX directly.
func RegisterTxConstructor(fn func(e *Engine, tx vl.Tx) AP.Transaction) {
	txNew = fn
}

// txwrap constructs the concrete *TX.Transaction via txNew.
func txwrap(e *Engine, tx vl.Tx) AP.Transaction {
	if txNew == nil {
		panic("sys/SY: RegisterTxConstructor not called; TX package not imported?")
	}
	return txNew(e, tx)
}
