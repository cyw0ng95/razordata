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

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
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
	log lg.Logger
	fs  *fs.FileManager
	lf  *lf.SegmentManager
	df  *df.BlockDevice
	sp  sp.SyncPool
	bp  bf.BufferPool
	wr  wr.Writer
	fl  fl.Flusher
	rp  rp.Replayer
	eng *ls.Engine
	txn *vl.Manager
	exe *executor.Executor
	// catalog is the persistent system catalog (iter-12). It
	// outlives the SQL executor's lifetime and is bound into
	// the EX package via SetCatalog at Open.
	catalog *ls.Catalog

	// Lifecycle.
	mu      sync.Mutex
	closed  atomic.Bool
	opened  atomic.Bool
	started time.Time
	// exeAdapter bridges the LS engine to the EX Store interface.
	exeAdapter *executorStoreAdapter
	// lastShutdown captures the outcome of the most recent
	// Shutdown() call. Populated by runShutdown, surfaced via
	// Stats() for post-mortem observability.
	lastShutdown AP.ShutdownStats
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
	if err := validateOptions(&opts); err != nil {
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

	// Step 11: VL TxnManager.
	e.txn = vl.NewManager()

	// Step 12: SQL executor.
	e.exeAdapter = &executorStoreAdapter{eng: e.eng}
	e.exe = executor.NewExecutorWithEngine(e.exeAdapter)

	// Step 13: System catalog (iter-12). Must come after the
	// executor is constructed so the EX package can be bound.
	if err := e.openCatalog(); err != nil {
		return err
	}

	e.started = time.Now()
	e.opened.Store(true)
	success = true
	return nil
}

// Close flushes pending writes and tears down every subsystem in
// reverse construction order. Safe to call multiple times.
func (e *Engine) Close(ctx context.Context) error {
	return e.Shutdown(ctx)
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
	// Reverse order: CATALOG → TXN → EX → ENG → RP → FL → WR → BF → DF → LF → FS.
	stop("catalog", func() error {
		e.closeCatalog()
		return nil
	})
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

// IsClosed reports whether Close has been called on this engine. It
// is the post-Close guard for Session/Transaction/Stmt methods and
// is goroutine-safe. The flag is flipped at the very start of
// Close, before any teardown work begins; callers observing true
// can rely on Close having committed to the shutdown sequence even
// if subsystem Close methods are still running in the background.
func (e *Engine) IsClosed() bool { return e.closed.Load() }

// Open is a no-op on an already-constructed *Engine; it exists to
// satisfy the AP.Engine interface. New callers should use the
// package-level Open constructor.
func (e *Engine) Open(ctx context.Context, dir string, opts AP.Options) error {
	if e.closed.Load() {
		return AP.ErrClosed
	}
	if e.opened.Load() {
		return AP.ErrAlreadyOpen
	}
	return AP.ErrNotOpen
}

// Stats aggregates metrics from every subsystem.
func (e *Engine) Stats() AP.EngineStats {
	lastShutdownMu.Lock()
	snap := e.lastShutdown
	lastShutdownMu.Unlock()
	if !e.opened.Load() {
		return AP.EngineStats{Version: AP.Version, LastShutdown: snap}
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
		WAL: e.walStats(),
		Tx: AP.TxnStats{
			Active:    tx.Active,
			Committed: tx.Committed,
			Aborted:   tx.Aborted,
		},
		LastShutdown: snap,
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

// walStats surfaces the replayer's counters into AP.WALStats so
// Engine.Stats() can show what the most recent Replay observed.
// Returns a zero-value struct if the replayer is not yet
// constructed (e.g. Stats() called before Open completes).
func (e *Engine) walStats() AP.WALStats {
	if e.rp == nil {
		return AP.WALStats{}
	}
	rs := e.rp.Stats()
	return AP.WALStats{
		TruncatedSegments:  int64(rs.TruncatedSegments),
		UnknownRecords:     int64(rs.UnknownRecords),
		CorruptionFailures: int64(rs.CorruptionFailures),
	}
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
