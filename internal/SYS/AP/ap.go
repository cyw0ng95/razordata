package AP

import (
	"sync/atomic"
	"context"
	"errors"
	"log/slog"
	"time"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SYS/BK"
)

const Version = "0.8.1"

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
	ShutdownTimeout    time.Duration // REQ000687: configurable shutdown timeout, default 30s
}

const (
	DefaultPageSize     = 4096
	DefaultMemTableSize = 64 * 1024 * 1024
	DefaultBufferPoolMB = 256
	DefaultWALSizeMB    = 64
	DefaultMaxLevel     = 7
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
	Types []int
	Data  []any
}

type Rows struct {
	cols   []string
	types  []int
	next   func() (Row, error)
	closer func() error
	closed atomic.Bool
}

func NewRows(cols []string, types []int, next func() (Row, error), closer func() error) *Rows {
	return &Rows{cols: cols, types: types, next: next, closer: closer}
}

func (r *Rows) Cols() []string  { return r.cols }
func (r *Rows) Types() []int    { return r.types }

func (r *Rows) Next() (Row, error) {
	if r == nil {
		return Row{}, ErrNoRows
	}
	if r.closed.Load() {
		return Row{}, ErrNoRows
	}
	if r.next == nil {
		r.closed.Store(true)
		return Row{}, ErrNoRows
	}
	row, err := r.next()
	if err != nil {
		if err == ErrNoRows {
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

var ErrNoRows = errors.New("ap: no more rows")

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

var (
	ErrNotFound         = New(KindNotFound, "key not found")
	ErrDuplicateKey     = New(KindDuplicateKey, "duplicate key")
	ErrLocked           = New(KindLocked, "resource locked")
	ErrCorrupt          = New(KindCorrupt, "data corrupt")
	ErrSyntax           = New(KindSyntax, "syntax error")
	ErrTypeMismatch     = New(KindTypeMismatch, "type mismatch")
	ErrTxAborted        = New(KindTxAborted, "transaction aborted")
	ErrIO               = New(KindIO, "I/O error")
	ErrUpgradeRequired  = New(KindUpgradeRequired, "upgrade required")
	ErrReadOnly         = New(KindReadOnly, "read-only")
	ErrDeadlineExceeded = New(KindDeadlineExceeded, "deadline exceeded")
	ErrAlreadyOpen      = New(KindInvalidOptions, "engine already open")
	ErrNotOpen          = New(KindClosed, "engine not open")
	ErrClosed           = New(KindClosed, "engine closed")
	ErrInvalidOptions   = New(KindInvalidOptions, "invalid options")
	ErrNoActiveTxn      = New(KindConstraint, "no active transaction")
	ErrUnknownSavepoint = New(KindConstraint, "unknown savepoint")
	ErrConstraint       = New(KindConstraint, "constraint violation")
)

func (r *Row) Len() int { return len(r.Cols) }

type BackupOptions = BK.BackupOptions
type BackupStats = BK.BackupStats
type RestoreStats = BK.RestoreStats

func Backup(ctx context.Context, srcDir, dstDir string, options BackupOptions) (*BackupStats, error) {
	return BK.Backup(ctx, srcDir, dstDir, options)
}

func Restore(ctx context.Context, backupDir, restoreDir string) (*RestoreStats, error) {
	return BK.Restore(ctx, backupDir, restoreDir)
}
