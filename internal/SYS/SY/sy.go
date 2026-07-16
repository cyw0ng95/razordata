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
	nm "github.com/cyw0ng95/razordata/internal/ENG/NM"
	df "github.com/cyw0ng95/razordata/internal/FIL/DF"
	fs "github.com/cyw0ng95/razordata/internal/FIL/FS"
	lf "github.com/cyw0ng95/razordata/internal/FIL/LF"
	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
	lg "github.com/cyw0ng95/razordata/internal/LOG/LG"
	bf "github.com/cyw0ng95/razordata/internal/MEM/BF"
	sp "github.com/cyw0ng95/razordata/internal/MEM/SP"
	executor "github.com/cyw0ng95/razordata/internal/SQB/EX"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	vl "github.com/cyw0ng95/razordata/internal/TXN/VL"
	fl "github.com/cyw0ng95/razordata/internal/WAL/FL"
	rp "github.com/cyw0ng95/razordata/internal/WAL/RP"
	wr "github.com/cyw0ng95/razordata/internal/WAL/WR"
)

type Engine struct {
	dir      string
	opts     AP.Options
	log      lg.Logger
	fs       *fs.FileManager
	lf       *lf.SegmentManager
	df       *df.BlockDevice
	sp       sp.SyncPool
	bp       bf.BufferPool
	wr       wr.Writer
	fl       fl.Flusher
	rp       rp.Replayer
	eng      *ls.Engine
	txn      *vl.Manager
	exe      *executor.Executor
	catalog  *ls.Catalog
	debugger interface{} // core.Debugger when debug tag active, nil otherwise

	// rowArena is a persistent bump-pointer allocator reused across all
	// queries in this Engine. Stored as a pointer so Executor ShallowCopy
	// clones can share it via a double-pointer (REQ001419). Eliminates
	// per-query 1 MB slab allocation that was 80% of SELECT WHERE memory.
	rowArena *DT.RowArena

	mu           sync.Mutex
	closed       atomic.Bool
	opened       atomic.Bool
	started      time.Time
	exeAdapter   *executorStoreAdapter
	lastShutdown AP.ShutdownStats
}

func Open(ctx context.Context, dir string, opts AP.Options) (*Engine, error) {
	if !opts.InMemory && dir == "" {
		return nil, fmt.Errorf("%w: dir is required", AP.New(AP.KindInvalidOptions, "invalid options"))
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
	if !o.CreateIfMissingSet {
		o.CreateIfMissing = true
		o.CreateIfMissingSet = true
	}
}

func (e *Engine) open(ctx context.Context) (err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	EC.BUG_ON(e.opened.Load(), "engine.open: double open without Close")
	if e.opened.Load() {
		return AP.New(AP.KindInvalidOptions, "engine already open")
	}
	executor.UnregisterAll()
	if e.opts.InMemory {
		return e.openInMemory()
	}
	if e.opts.CreateIfMissing {
		if err := os.MkdirAll(e.dir, 0o755); err != nil {
			return fmt.Errorf("sy: mkdir %s: %w", e.dir, err)
		}
	} else {
		if _, err := os.Stat(e.dir); err != nil {
			return fmt.Errorf("sy: stat %s: %w", e.dir, err)
		}
	}
	e.log = lg.New(lg.Options{Format: e.opts.LogFormat, Level: e.opts.LogLevel, Output: os.Stderr})
	success := false
	defer func() {
		if !success {
			e.closeBestEffort()
		}
	}()
	fsRoot := filepath.Join(e.dir, "fil")
	if err := os.MkdirAll(fsRoot, 0o755); err != nil {
		return err
	}
	if e.fs, err = fs.NewOrCreate(fsRoot, e.log); err != nil {
		return err
	}
	walDir := filepath.Join(e.dir, "wal")
	if err := os.MkdirAll(walDir, 0o755); err != nil {
		return err
	}
	if e.lf, err = lf.New(walDir, e.log); err != nil {
		return err
	}
	bdPath := filepath.Join(e.dir, "meta.razor")
	if e.opts.ReadOnly {
		if e.df, err = df.OpenReadOnly(bdPath, e.log); err != nil {
			return err
		}
	} else if e.opts.CreateIfMissing {
		if e.df, err = df.Create(bdPath, e.log); err != nil {
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
	e.sp = sp.NewWithOptions(sp.Options{EnableHugePages: e.opts.EnableHugePages})
	bpCapacity := int64(e.opts.BufferPoolMB) * 1024 * 1024
	if e.bp, err = bf.NewWithOptions(bpCapacity, filepath.Join(e.dir, "bp.hint"), e.df, e.sp, bf.Options{GetNode: nm.CurrentNode}, e.log); err != nil {
		return err
	}
	if e.wr, err = wr.New(walDir, e.lf, e.sp, e.log, e.opts.ReadOnly); err != nil {
		return err
	}
	if e.fl, err = fl.New(walDir, e.lf, e.fs, e.log); err != nil {
		return err
	}
	if e.rp, err = rp.New(walDir, e.lf, e.bp, rp.Callbacks{}, e.log); err != nil {
		return err
	}
	if err := e.rp.Replay(); err != nil && !errors.Is(err, os.ErrNotExist) {
		e.log.Warn("wal.replay", "err", err)
	}
	if e.eng, err = ls.OpenWithOptions(filepath.Join(e.dir, "eng"), ls.Options{
		MemTableShards: ls.DefaultMemTableShards,
		MemTableSize:   ls.DefaultMemTableSize,
		MmapFiles:      e.opts.MmapFiles,
		BlockCacheSize: e.opts.BlockCacheSize,
		SmallTableRows: int64(e.opts.SmallTableRows),
	}); err != nil {
		return err
	}
	e.txn = vl.NewManager()
	e.exeAdapter = &executorStoreAdapter{eng: e.eng}
	e.exe = executor.NewExecutorWithEngine(e.exeAdapter)
	e.exe.SetRowArena(&e.rowArena)
	e.exe.WithMemoryBudget(e.opts.MaxMemoryPerQuery, e.opts.JoinBufferSize)
	if e.opts.MaxResultRows > 0 {
		e.exe.WithMaxResultRows(e.opts.MaxResultRows)
	}
	if err := e.openCatalog(); err != nil {
		return err
	}
	e.initDebugger()
	e.started = time.Now()
	e.opened.Store(true)
	success = true
	return nil
}

func (e *Engine) openInMemory() (err error) {
	e.log = lg.New(lg.Options{Format: "text", Level: e.opts.LogLevel, Output: os.Stderr})
	success := false
	defer func() {
		if !success {
			e.closeBestEffort()
		}
	}()
	e.sp = sp.NewWithOptions(sp.Options{EnableHugePages: e.opts.EnableHugePages})
	e.exe = executor.NewExecutor()
	e.exe.SetRowArena(&e.rowArena)
	e.exe.WithMemoryBudget(e.opts.MaxMemoryPerQuery, e.opts.JoinBufferSize)
	if e.opts.MaxResultRows > 0 {
		e.exe.WithMaxResultRows(e.opts.MaxResultRows)
	}
	e.txn = vl.NewManager()
	e.started = time.Now()
	e.opened.Store(true)
	success = true
	return nil
}

func (e *Engine) SetSnapshot(ts uint64) {
	e.exeAdapter.SetSnapshot(ts)
	e.exe.SetSnapshot(ts)
}

func (e *Engine) CurrentTS() uint64 { return vl.GetCurrentTS() }

func (e *Engine) Close(ctx context.Context) error { return e.Shutdown(ctx) }

func (e *Engine) closeBestEffort() error {
	if e == nil {
		return nil
	}
	var firstErr error
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
	stop("catalog", func() error { e.closeCatalog(); return nil })
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
	stop("exe", func() error {
		if e.exe == nil {
			return nil
		}
		e.exe.Close()
		return nil
	})
	return firstErr
}

var sessionConstructor func(e *Engine) AP.Session

func RegisterSession(fn func(e *Engine) AP.Session) { sessionConstructor = fn }

func (e *Engine) Begin(ctx context.Context) (AP.Session, error) {
	if !e.opened.Load() {
		return nil, AP.New(AP.KindClosed, "engine not open")
	}
	EC.WARN_ON(e.closed.Load(), "engine.Begin: use-after-close")
	if e.closed.Load() {
		return nil, AP.New(AP.KindClosed, "engine closed")
	}
	if sessionConstructor == nil {
		return nil, AP.New(AP.KindClosed, "engine closed")
	}
	return sessionConstructor(e), nil
}

func (e *Engine) IsClosed() bool   { return e.closed.Load() }

// Reset drops all user tables, schemas, and in-memory state, returning
// the engine to a clean post-Open state. Preserves the directory, WAL,
// and storage engine; only the logical catalog is reset. REQ001454.
func (e *Engine) Reset(ctx context.Context) error {
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()
	// Clear in-memory tables and schemas.
	for k := range DT.Tables {
		delete(DT.Tables, k)
	}
	for k := range DT.Schemas {
		delete(DT.Schemas, k)
	}
	for k := range DT.InMemSchemas {
		delete(DT.InMemSchemas, k)
	}
	// Reset the executor's plan cache.
	e.exe.ClearPlanCache()
	// Reset the per-engine row arena for fresh re-use.
	if e.rowArena != nil {
		e.rowArena.Reset()
	}
	// REQ001497: reset the page cache to reclaim memory between resets.
	if e.eng != nil {
		e.eng.ResetPageCache()
	}
	return nil
}
func (e *Engine) IsReadOnly() bool { return e.opts.ReadOnly }

func (e *Engine) Open(ctx context.Context, dir string, opts AP.Options) error {
	if e.closed.Load() {
		return AP.New(AP.KindClosed, "engine closed")
	}
	EC.BUG_ON(e.opened.Load(), "engine.Open: double open without Close")
	if e.opened.Load() {
		return AP.New(AP.KindInvalidOptions, "engine already open")
	}
	return AP.New(AP.KindClosed, "engine not open")
}

func (e *Engine) Stats() AP.EngineStats {
	lastShutdownMu.Lock()
	snap := e.lastShutdown
	lastShutdownMu.Unlock()
	if !e.opened.Load() {
		return AP.EngineStats{Version: AP.Version, LastShutdown: snap}
	}
	uptime := time.Duration(0)
	if !e.started.IsZero() {
		uptime = time.Since(e.started)
	}
	out := AP.EngineStats{Version: AP.Version, Uptime: uptime, LastShutdown: snap}
	if e.eng != nil {
		out.LSMTree = e.eng.Stats()
	}
	if e.bp != nil {
		bp := e.bp.Stats()
		out.BufferPool = AP.BufferPoolStats{Hits: bp.Hits, Misses: bp.Misses, Evictions: bp.Evicts}
	}
	if e.txn != nil {
		tx := e.txn.Stats()
		out.Tx = AP.TxnStats{Active: tx.Active, Committed: tx.Committed, Aborted: tx.Aborted}
	}
	if !e.opts.InMemory {
		out.WAL = e.walStats()
	}
	return out
}

// RegisterCollation registers a user-defined collation function.
// REQ001332.
func (e *Engine) RegisterCollation(name string, fn AP.CollateFunc) error {
	return e.exe.RegisterCollation(name, fn)
}

type executorStoreAdapter struct {
	eng        *ls.Engine
	snapshotTS uint64
}

func (a *executorStoreAdapter) Insert(k, v []byte) error { return a.eng.Insert(k, v) }
func (a *executorStoreAdapter) Delete(k []byte) error    { return a.eng.Delete(k) }
func (a *executorStoreAdapter) Get(k []byte) ([]byte, bool, error) {
	v, err := a.eng.Get(k)
	if err != nil {
		if errors.Is(err, ls.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return v, true, nil
}
func (a *executorStoreAdapter) NewIterator(prefix []byte) ls.RangeIter {
	return a.eng.NewIterator(prefix)
}
func (a *executorStoreAdapter) ManualCompact() error  { return a.eng.ManualCompact() }
func (a *executorStoreAdapter) SetSnapshot(ts uint64) { a.snapshotTS = ts }

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

func (e *Engine) Executor() *executor.Executor { return e.exe.ShallowCopy() }

// MaxResultRows returns the per-query result row limit.
// 0 means unlimited. REQ001056.
func (e *Engine) MaxResultRows() int64 {
	if e.exe == nil {
		return 0
	}
	return e.exe.MaxResultRows()
}

func (e *Engine) ExtractParamTypes(sql string) []int {
	if e.exe == nil {
		return nil
	}
	return e.exe.ExtractParamTypes(sql)
}

func (e *Engine) Engine() *ls.Engine           { return e.eng }
func (e *Engine) TxnManager() *vl.Manager      { return e.txn }
func (e *Engine) FileManager() *fs.FileManager { return e.fs }
func (e *Engine) BufferPool() bf.BufferPool    { return e.bp }
func (e *Engine) Writer() wr.Writer            { return e.wr }
func (e *Engine) Flusher() fl.Flusher          { return e.fl }
func (e *Engine) Replayer() rp.Replayer        { return e.rp }
func (e *Engine) Logger() lg.Logger            { return e.log }

func (e *Engine) BeginTxn(ctx context.Context) (AP.Transaction, error) {
	if e.closed.Load() {
		return nil, AP.New(AP.KindClosed, "engine closed")
	}
	if e.txn == nil {
		return nil, fmt.Errorf("txn manager not initialized")
	}
	tx, err := e.txn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if !e.opts.ReadOnly && e.wr != nil {
		if w, ok := tx.(interface{ WithWAL(vl.WALWriter) vl.Tx }); ok {
			tx = w.WithWAL(e.wr)
		}
	}
	return txwrap(e, tx), nil
}

var txNew func(e *Engine, tx vl.Tx) AP.Transaction

func RegisterTxConstructor(fn func(e *Engine, tx vl.Tx) AP.Transaction) { txNew = fn }

func txwrap(e *Engine, tx vl.Tx) AP.Transaction {
	if txNew == nil {
		panic("sys/SY: RegisterTxConstructor not called; TX package not imported?")
	}
	return txNew(e, tx)
}
