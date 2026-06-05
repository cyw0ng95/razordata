// Package AP defines the public razordata API surface: Engine,
// Session, Transaction, Stmt, Options, error types, and stats. All other
// SYS clusters (SY, SE, TX, ST) implement or return values of these
// types.
package AP

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Version is the razordata release version, surfaced in EngineStats.
const Version = "0.6.2"

// Options configures an Engine opened via Open. Zero values for tunable
// fields are filled with package defaults in SY.Open.
type Options struct {
	// Dir is the database directory (required).
	Dir string
	// PageSize is the block size in bytes; must be a power of two.
	// Default 4096.
	PageSize int
	// MemTableSize is the memtable flush threshold in bytes.
	// Default 64 MiB.
	MemTableSize int
	// BufferPoolMB is the buffer pool size in MiB.
	// Default 256 MiB.
	BufferPoolMB int
	// WALSizeMB is the WAL segment size in MiB.
	// Default 64 MiB.
	WALSizeMB int
	// MaxLevel is the maximum number of LSM levels.
	// Default 7.
	MaxLevel int
	// LogLevel is the minimum emitted level.
	// Default slog.LevelInfo.
	LogLevel slog.Level
	// LogFormat is "text" or "json".
	// Default "text".
	LogFormat string
	// ReadOnly opens the database in read-only mode.
	// Default false.
	ReadOnly bool
	// CreateIfMissing creates the database directory if it does not
	// exist. Default true. Tracked with CreateIfMissingSet to
	// distinguish "unset" from "explicitly false" during option
	// defaulting.
	CreateIfMissing    bool
	CreateIfMissingSet bool
}

// Default values applied by SY.Open when Options leaves a field at its
// zero value.
const (
	DefaultPageSize     = 4096
	DefaultMemTableSize = 64 * 1024 * 1024
	DefaultBufferPoolMB = 256
	DefaultWALSizeMB    = 64
	DefaultMaxLevel     = 7
)

// Engine is the top-level database handle. The zero value is not
// usable; obtain one via Open.
type Engine interface {
	// Open validates Options, constructs every subsystem in dependency
	// order, and replays the WAL. It is invoked once after construction;
	// calling it again returns ErrAlreadyOpen.
	Open(ctx context.Context, dir string, opts Options) error
	// Close flushes pending writes and tears down every subsystem in
	// reverse construction order. Safe to call multiple times.
	Close(ctx context.Context) error
	// Begin returns a fresh Session. Sessions are not goroutine-shared
	// unless documented otherwise.
	Begin(ctx context.Context) (Session, error)
	// Stats returns aggregate subsystem metrics.
	Stats() EngineStats
}

// Session is a single client-side connection.
type Session interface {
	Query(ctx context.Context, sql string, args ...any) (*Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (Result, error)
	Begin(ctx context.Context) (Transaction, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
	SetDeadline(deadline time.Time) error
	Stats() SessionStats
}

// Transaction is the session's active transaction. While a transaction
// is open, the session refuses to begin a new one (returns ErrLocked).
type Transaction interface {
	Query(ctx context.Context, sql string, args ...any) (*Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (Result, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
	Savepoint(ctx context.Context, name string) error
	RollbackTo(ctx context.Context, name string) error
}

// Stmt is a prepared statement. The same plan is reused across
// invocations; only the bound arguments vary.
type Stmt interface {
	Query(ctx context.Context, args ...any) (*Rows, error)
	Exec(ctx context.Context, args ...any) (Result, error)
	Close() error
}

// Result summarizes the outcome of an Exec call.
type Result struct {
	RowsAffected int64
	LastInsertID uint64
}

// Rows describes the schema of a query result. The actual rows are
// streamed to the caller through a separate type returned by Query.
type Rows struct {
	Cols  []string
	Types []int
}

// EngineStats is an aggregate of per-subsystem statistics.
type EngineStats struct {
	Version    string
	Uptime     time.Duration
	LSMTree    LSMTreeStats
	BufferPool BufferPoolStats
	WAL        WALStats
	Tx         TxnStats
}

// LSMTreeStats summarizes the LSM engine's read-side counters.
type LSMTreeStats struct {
	MemtableHits int64
	SSTHits      int64
	DiskReads    int64
}

// BufferPoolStats summarizes the page cache.
type BufferPoolStats struct {
	Hits      int64
	Misses    int64
	Evictions int64
}

// WALStats summarizes the write-ahead log.
type WALStats struct {
	RecordsWritten int64
	BytesWritten   int64
	Syncs          int64
	CurrentLSN     uint64
}

// TxnStats summarizes the transaction manager.
type TxnStats struct {
	Active    int
	Committed int64
	Aborted   int64
}

// SessionStats summarizes a single session's lifetime counters.
type SessionStats struct {
	ID           uint64
	QueryCount   int64
	RowsReturned int64
	BytesRead    int64
	BytesWritten int64
	ActiveTXN    bool
}

// Error sentinels. Callers use errors.Is to map low-level failures to
// retry strategy: ErrIO and ErrLocked are retryable; the rest are
// fatal.
var (
	ErrNotFound         = errors.New("razordata: key not found")
	ErrDuplicateKey     = errors.New("razordata: duplicate key")
	ErrLocked           = errors.New("razordata: resource locked")
	ErrCorrupt          = errors.New("razordata: data corrupt")
	ErrSyntax           = errors.New("razordata: syntax error")
	ErrTypeMismatch     = errors.New("razordata: type mismatch")
	ErrTxAborted        = errors.New("razordata: transaction aborted")
	ErrIO               = errors.New("razordata: I/O error")
	ErrUpgradeRequired  = errors.New("razordata: upgrade required")
	ErrReadOnly         = errors.New("razordata: read-only")
	ErrDeadlineExceeded = errors.New("razordata: deadline exceeded")
	ErrAlreadyOpen      = errors.New("razordata: engine already open")
	ErrNotOpen          = errors.New("razordata: engine not open")
	ErrClosed           = errors.New("razordata: engine closed")
	ErrInvalidOptions   = errors.New("razordata: invalid options")
	ErrNoActiveTxn      = errors.New("razordata: no active transaction")
	ErrUnknownSavepoint = errors.New("razordata: unknown savepoint")
)

// RetryableErrors is the set of sentinels callers should treat as
// retryable. ErrLocked retries should use exponential backoff.
var RetryableErrors = []error{ErrIO, ErrLocked}

// FatalErrors is the set of sentinels that must not be retried.
var FatalErrors = []error{
	ErrTxAborted, ErrCorrupt, ErrSyntax, ErrTypeMismatch,
	ErrUpgradeRequired, ErrReadOnly, ErrAlreadyOpen, ErrNotOpen,
	ErrClosed, ErrInvalidOptions, ErrNoActiveTxn, ErrUnknownSavepoint,
	ErrNotFound, ErrDuplicateKey, ErrDeadlineExceeded,
}

// IsRetryable reports whether err is one of the retryable sentinels
// (wrapped errors are unwrapped via errors.Is).
func IsRetryable(err error) bool {
	for _, target := range RetryableErrors {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// IsFatal reports whether err is one of the fatal sentinels.
func IsFatal(err error) bool {
	for _, target := range FatalErrors {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// Row is a materialized query row. Cols and Types are aligned with
// Data. Use Len() to know the column count.
type Row struct {
	Cols  []string
	Types []int
	Data  []any
}

// Len returns the number of columns.
func (r *Row) Len() int { return len(r.Cols) }
