package AP

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SYS/BK"
	CT "github.com/cyw0ng95/razordata/internal/SYS/CT"
)

const Version = "0.9.0"

// CollateFunc is a user-registered collation function for ORDER BY / COMPARE.
// REQ001332. REQ002085: aliased from SYS/CT.
type CollateFunc = CT.CollateFunc

type Options struct {
	Dir                string
	PageSize           int
	MemTableSize       int
	BufferPoolMB       int
	WALSizeMB          int
	MaxLevel           int
	LogLevel           slog.Level
	LogFormat          string
	ReadOnly           bool
	CreateIfMissing    bool
	CreateIfMissingSet bool
	InMemory           bool
	MemoryOnly         bool          // REQ002071: full engine with in-memory storage, no WAL/SST/disk I/O
	ShutdownTimeout    time.Duration // REQ000687: configurable shutdown timeout, default 30s
	EmergencyShutdown  bool          // REQ000688: skip Phase 2+3, flush only
	MaxMemoryPerQuery  int64         // REQ001056: per-query memory cap, 0 = unlimited
	JoinBufferSize     int64         // REQ001056: per-hash-join memory cap, 0 = unlimited
	MaxResultRows      int64         // REQ001056: per-query result row cap, 0 = unlimited

	// Performance tuning
	EnableHugePages bool // REQ001207: use 2MB hugetlbfs for buffer pool
	MmapFiles       bool // REQ001227: zero-copy reads via mmap
	BlockCacheSize  int  // REQ001242: decompressed SST block cache; 0 = disabled, negative = default (1024)
	SmallTableRows  int  // REQ001244: tables with ≤N rows served from memtables only; 0 = disabled, negative = default (256)

	// Debug options — parsed but only acted on with -tags debug.
	DebugDir           string
	EnableDebugSocket  bool
	EnableDebugSignals bool
	TraceEventCapacity int
	SlowQueryThreshold time.Duration

	// REQ001304: LSM auto-compaction settings.
	AutoCompactMode    string // "none" | "incremental" | "full", default "none"
	AutoCompactThreshold float64 // garbage ratio threshold, default 0.3
}

const (
	DefaultPageSize        = 4096
	DefaultMemTableSize    = 128 * 1024 * 1024
	DefaultBufferPoolMB    = 512
	DefaultWALSizeMB       = 256
	DefaultMaxLevel        = 7
	DefaultShutdownTimeout = 30 * time.Second // REQ000687
)

type IsolationLevel int

const (
	IsolationReadUncommitted IsolationLevel = iota
	IsolationReadCommitted
	IsolationRepeatableRead
	IsolationSerializable
)

func (il IsolationLevel) String() string {
	switch il {
	case IsolationReadUncommitted:
		return "READ UNCOMMITTED"
	case IsolationReadCommitted:
		return "READ COMMITTED"
	case IsolationRepeatableRead:
		return "REPEATABLE READ"
	case IsolationSerializable:
		return "SERIALIZABLE"
	default:
		return "UNKNOWN"
	}
}

func ParseIsolationLevel(s string) (IsolationLevel, bool) {
	switch s {
	case "READ UNCOMMITTED":
		return IsolationReadUncommitted, true
	case "READ COMMITTED":
		return IsolationReadCommitted, true
	case "REPEATABLE READ":
		return IsolationRepeatableRead, true
	case "SERIALIZABLE":
		return IsolationSerializable, true
	default:
		return 0, false
	}
}

type Engine interface {
	Open(ctx context.Context, dir string, opts Options) error
	Close(ctx context.Context) error
	Begin(ctx context.Context) (Session, error)
	Stats() EngineStats
	RegisterCollation(name string, fn CollateFunc) error // REQ001332
}

type Session interface {
	Query(ctx context.Context, sql string, args ...any) (*Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (Result, error)
	Begin(ctx context.Context) (Transaction, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
	SetDeadline(deadline time.Time) error
	Stats() SessionStats
}

type Transaction interface {
	Query(ctx context.Context, sql string, args ...any) (*Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (Result, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
	Savepoint(ctx context.Context, name string) error
	ReleaseSavepoint(ctx context.Context, name string) error
	RollbackTo(ctx context.Context, name string) error
}

type Stmt interface {
	Query(ctx context.Context, args ...any) (*Rows, error)
	Exec(ctx context.Context, args ...any) (Result, error)
	Close() error
}

type Result struct {
	RowsAffected int64
	LastInsertID uint64
}

type Row struct {
	Cols  []string
	Types []LX.TokenType
	Data  []Value
}

type Rows struct {
	cols   []string
	types  []LX.TokenType
	next   func() (Row, error)
	closer func() error
	closed atomic.Bool
}

func NewRows(cols []string, types []LX.TokenType, next func() (Row, error), closer func() error) *Rows {
	return &Rows{cols: cols, types: types, next: next, closer: closer}
}

func (r *Rows) Cols() []string        { return r.cols }
func (r *Rows) Types() []LX.TokenType { return r.types }

func (r *Rows) Next() (Row, error) {
	if r == nil {
		return Row{}, New(KindNotFound, "no more rows")
	}
	if r.closed.Load() {
		return Row{}, New(KindNotFound, "no more rows")
	}
	if r.next == nil {
		r.closed.Store(true)
		return Row{}, New(KindNotFound, "no more rows")
	}
	row, err := r.next()
	if err != nil {
		noRows := New(KindNotFound, "no more rows")
		if err.Error() == noRows.Message && IsKind(err, KindNotFound) {
			r.closed.Store(true)
		}
		return Row{}, err
	}
	return row, nil
}

func (r *Rows) Close() error {
	if r == nil {
		return nil
	}
	if r.closer != nil {
		_ = r.closer()
	}
	r.closed.Store(true)
	return nil
}

type EngineStats struct {
	Version      string
	Uptime       time.Duration
	LSMTree      ls.ReadStats
	BufferPool   BufferPoolStats
	WAL          WALStats
	Tx           TxnStats
	LastShutdown ShutdownStats
}

type ShutdownStats struct {
	At              time.Time `json:"at"`
	DurationMS      int64     `json:"duration_ms"`
	ForceAborted    int       `json:"force_aborted"`
	BackgroundStops int       `json:"background_stops"`
	UptimeSeconds   int64     `json:"uptime_seconds"`
	FirstError      string    `json:"first_error,omitempty"`
}

type BufferPoolStats struct {
	Hits      int64
	Misses    int64
	Evictions int64
}

type WALStats struct {
	RecordsWritten     int64
	BytesWritten       int64
	Syncs              int64
	CurrentLSN         uint64
	TruncatedSegments  int64
	UnknownRecords     int64
	CorruptionFailures int64
}

type TxnStats struct {
	Active    int
	Committed int64
	Aborted   int64
}

type SessionStats struct {
	ID           uint64
	QueryCount   int64
	RowsReturned int64
	BytesRead    int64
	BytesWritten int64
	ActiveTXN    bool
}

// Deprecated: re-exported from LOG/EC for backward compatibility.
var (
	ErrNoActiveTxn      = EC.New(EC.KindConstraint, "no active transaction")
	ErrUnknownSavepoint = EC.New(EC.KindConstraint, "unknown savepoint")
	ErrConstraint       = EC.New(EC.KindConstraint, "constraint violation")
)

func (r *Row) Len() int { return len(r.Cols) }

// Re-exported error types from LOG/EC for backward compatibility.
// New code should import LOG/EC directly.
type Error = EC.Error
type Kind = EC.Kind

// Re-exported Kind constants from LOG/EC.
const (
	// Deprecated: use EC.Kind* directly.
	KindNotFound          Kind = EC.KindNotFound
	KindDuplicateKey      Kind = EC.KindDuplicateKey
	KindLocked            Kind = EC.KindLocked
	KindCorrupt           Kind = EC.KindCorrupt
	KindSyntax            Kind = EC.KindSyntax
	KindTypeMismatch      Kind = EC.KindTypeMismatch
	KindTxAborted         Kind = EC.KindTxAborted
	KindIO                Kind = EC.KindIO
	KindUpgradeRequired   Kind = EC.KindUpgradeRequired
	KindReadOnly          Kind = EC.KindReadOnly
	KindDeadlineExceeded  Kind = EC.KindDeadlineExceeded
	KindConstraint        Kind = EC.KindConstraint
	KindClosed            Kind = EC.KindClosed
	KindInvalidOptions    Kind = EC.KindInvalidOptions
	KindInternal          Kind = EC.KindInternal
	KindNotImplemented    Kind = EC.KindNotImplemented
	KindConflict          Kind = EC.KindConflict
	KindResourceExhausted Kind = EC.KindResourceExhausted
	KindParse             Kind = EC.KindParse
)

// Re-exported Entity constants from LOG/EC.
const (
	EntityTable      = EC.EntityTable
	EntityColumn     = EC.EntityColumn
	EntityKey        = EC.EntityKey
	EntitySavepoint  = EC.EntitySavepoint
	EntityIndex      = EC.EntityIndex
	EntityView       = EC.EntityView
	EntityTrigger    = EC.EntityTrigger
	EntityConstraint = EC.EntityConstraint
	EntityRow        = EC.EntityRow
)

// Re-exported Layer constants from LOG/EC.
const (
	LayerSQL    EC.Layer = EC.LayerSQL
	LayerTXN    EC.Layer = EC.LayerTXN
	LayerENG    EC.Layer = EC.LayerENG
	LayerWAL    EC.Layer = EC.LayerWAL
	LayerFIL    EC.Layer = EC.LayerFIL
	LayerMEM    EC.Layer = EC.LayerMEM
	LayerLOG    EC.Layer = EC.LayerLOG
	LayerIO     EC.Layer = EC.LayerIO
	LayerConfig EC.Layer = EC.LayerConfig
	LayerINT    EC.Layer = EC.LayerINT
)

// Re-exported Op constants from LOG/EC.
const (
	OpSelect     = EC.OpSelect
	OpInsert     = EC.OpInsert
	OpUpdate     = EC.OpUpdate
	OpDelete     = EC.OpDelete
	OpCreate     = EC.OpCreate
	OpDrop       = EC.OpDrop
	OpAlter      = EC.OpAlter
	OpBegin      = EC.OpBegin
	OpCommit     = EC.OpCommit
	OpRollback   = EC.OpRollback
	OpSavepoint  = EC.OpSavepoint
	OpReplay     = EC.OpReplay
	OpFlush      = EC.OpFlush
	OpCompact    = EC.OpCompact
	OpCheckpoint = EC.OpCheckpoint
	OpBackup     = EC.OpBackup
	OpRestore    = EC.OpRestore
)

// Re-exported constructors from LOG/EC.
// Deprecated: import LOG/EC directly.
func New(kind Kind, msg string) *Error { return EC.New(kind, msg) }
func Wrap(kind Kind, err error) *Error { return EC.Wrap(kind, err) }

// Re-exported helpers from LOG/EC.
// Deprecated: import LOG/EC directly.
func IsKind(err error, kind Kind) bool       { return EC.IsKind(err, kind) }
func IsRetryable(err error) bool             { return EC.IsRetryable(err) }
func IsFatal(err error) bool                 { return EC.IsFatal(err) }
func AsError(err error, target **Error) bool { return EC.AsError(err, target) }

// Value kind constants for the tagged-union Value type (REQ000776/REQ000862).
// REQ002085: ValueKind, kind constants, and Value are aliased from SYS/CT.
const (
	KindNull  = CT.KindNull
	KindInt   = CT.KindInt
	KindFloat = CT.KindFloat
	KindText  = CT.KindText
	KindBlob  = CT.KindBlob
	KindBool  = CT.KindBool
)

type ValueKind = CT.ValueKind
type Value = CT.Value

// REQ002085: Value constructors re-exported from SYS/CT.
func NewIntValue(v int64) Value   { return Value{Kind: KindInt, I64: v} }
func NewFloatValue(v float64) Value { return Value{Kind: KindFloat, F64: v} }
func NewTextValue(v string) Value  { return Value{Kind: KindText, S: v} }
func NewBlobValue(v []byte) Value  { return Value{Kind: KindBlob, B: v} }
func NewBoolValue(v bool) Value    { return Value{Kind: KindBool, Bo: v} }
func NullValue() Value             { return Value{Kind: KindNull} }

type BackupOptions = BK.BackupOptions
type BackupStats = BK.BackupStats
type RestoreStats = BK.RestoreStats

func Backup(ctx context.Context, srcDir, dstDir string, options BackupOptions) (*BackupStats, error) {
	return BK.Backup(ctx, srcDir, dstDir, options)
}

func Restore(ctx context.Context, backupDir, restoreDir string) (*RestoreStats, error) {
	return BK.Restore(ctx, backupDir, restoreDir)
}
