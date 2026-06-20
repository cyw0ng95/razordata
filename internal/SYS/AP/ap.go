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

	"github.com/cyw0ng95/razordata/internal/SYS/BK"
)

// Version is the razordata release version, surfaced in EngineStats.
const Version = "0.8.1"

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
	// InMemory creates a purely in-memory database with no
	// persistent storage. When true, Dir is ignored (or must
	// be ":memory:"). All DDL/DML state lives in the EX
	// package's tables/schemas maps and is lost on Engine.Close.
	// WAL, SST, catalog, and filesystem I/O are all bypassed.
	// Default false.
	InMemory bool
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

// IsolationLevel represents a transaction isolation level (REQ000123).
type IsolationLevel int

const (
	IsolationReadUncommitted IsolationLevel = iota
	IsolationReadCommitted
	IsolationRepeatableRead
	IsolationSerializable
)

// String returns the SQL name of the isolation level.
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

// ParseIsolationLevel converts a SQL isolation level string to the enum.
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
	ReleaseSavepoint(ctx context.Context, name string) error
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

// Row is a single result row from a query. The Data slice holds the
// column values; Cols and Types describe the schema.
// REQ000348.
type Row struct {
	Cols  []string
	Types []int
	Data  []any
}

// Rows is a row iterator returned by Session.Query, Transaction.Query,
// and Stmt.Query. Callers must call Close when done to release
// underlying resources. After Next returns ErrNoRows, the iterator is
// auto-closed and the caller should NOT call Close.
// REQ000348.
type Rows struct {
	Cols  []string
	Types []int
	// next is the streaming callback. It returns the next row or
	// ErrNoRows to signal end-of-stream. It may also be nil for
	// stubs that have no real data (e.g. AP.Rows in some test paths).
	next func() (Row, error)
	// closer releases plan/executor resources.
	closer func() error
	// closed tracks whether Close has been called or the iterator
	// has been auto-closed via ErrNoRows.
	closed bool
}

// NewRows constructs a Rows with the given schema. The next and
// closer callbacks are optional.
func NewRows(cols []string, types []int, next func() (Row, error), closer func() error) *Rows {
	return &Rows{
		Cols:   cols,
		Types:  types,
		next:   next,
		closer: closer,
	}
}

// GetCols returns the column names of the result set.
func (r *Rows) GetCols() []string { return r.Cols }

// GetTypes returns the column type codes of the result set.
func (r *Rows) GetTypes() []int { return r.Types }

// Next returns the next row, or ErrNoRows when the result set is
// exhausted. The returned row's Cols and Types match the schema.
// REQ000348.
func (r *Rows) Next() (Row, error) {
	if r == nil {
		return Row{}, ErrNoRows
	}
	if r.closed {
		return Row{}, ErrNoRows
	}
	if r.next == nil {
		// Schema-only stub. Auto-close on first call.
		r.closed = true
		return Row{}, ErrNoRows
	}
	row, err := r.next()
	if err != nil {
		if err == ErrNoRows {
			r.closed = true
		}
		return Row{}, err
	}
	return row, nil
}

// Close releases the underlying plan resources. Safe to call multiple
// times. After Next returns ErrNoRows the iterator is auto-closed and
// this is a no-op.
// REQ000348.
func (r *Rows) Close() error {
	if r == nil {
		return nil
	}
	if r.closer != nil {
		_ = r.closer()
	}
	r.closed = true
	return nil
}

// ErrNoRows is returned by Rows.Next when the result set is exhausted.
// The iterator is automatically closed at that point.
var ErrNoRows = errors.New("ap: no more rows")

// EngineStats is an aggregate of per-subsystem statistics.
type EngineStats struct {
	Version      string
	Uptime       time.Duration
	LSMTree      LSMTreeStats
	BufferPool   BufferPoolStats
	WAL          WALStats
	Tx           TxnStats
	LastShutdown ShutdownStats
}

// ShutdownStats captures the outcome of the most recent
// graceful-shutdown sequence (SYS.md:215-282). Operators can read
// this via Engine.Stats() after a Close to see whether the
// shutdown was clean, whether transactions were force-aborted, and
// how long each phase took.
type ShutdownStats struct {
	At              time.Time `json:"at"`
	DurationMS      int64     `json:"duration_ms"`
	ForceAborted    int       `json:"force_aborted"`
	BackgroundStops int       `json:"background_stops"`
	UptimeSeconds   int64     `json:"uptime_seconds"`
	FirstError      string    `json:"first_error,omitempty"`
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
	// TruncatedSegments counts segments whose tail was dropped
	// at the end of the most recent Replay because the
	// records were torn. Surface via Engine.Stats so operators
	// can distinguish a clean restart from one that tolerated
	// a partial write at shutdown. Added in iter-15 (REQ000190).
	TruncatedSegments int64
	// UnknownRecords counts records whose type was not
	// recognised by the current reader; skipped on the
	// forward-compat path. See WAL/RP/rp.go and TXN.md.
	UnknownRecords int64
	// CorruptionFailures counts records that failed the
	// envelope CRC; each is a mid-segment corruption that
	// aborted the replay. Non-zero here is a strong signal
	// that the WAL is damaged and the database needs repair.
	CorruptionFailures int64
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
	ErrConstraint       = errors.New("razordata: constraint violation")
)

// RetryableErrors is the set of sentinels callers should treat as
// retryable. ErrLocked retries should use exponential backoff.
var RetryableErrors = []error{ErrIO, ErrLocked}

// FatalErrors is the set of sentinels that must not be retried.
var FatalErrors = []error{
	ErrTxAborted, ErrCorrupt, ErrSyntax, ErrTypeMismatch,
	ErrUpgradeRequired, ErrReadOnly, ErrAlreadyOpen, ErrNotOpen,
	ErrClosed, ErrInvalidOptions, ErrNoActiveTxn, ErrUnknownSavepoint,
	ErrNotFound, ErrDuplicateKey, ErrDeadlineExceeded, ErrConstraint,
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

// Len returns the number of columns.
func (r *Row) Len() int { return len(r.Cols) }

// REQ000259: Public backup and restore API. The functions are
// re-exported from the BK package so callers can use them via the
// AP namespace without importing the internal BK package directly.

// BackupOptions configures a backup run.
type BackupOptions = BK.BackupOptions

// BackupStats summarizes the outcome of a backup.
type BackupStats = BK.BackupStats

// RestoreStats summarizes the outcome of a restore.
type RestoreStats = BK.RestoreStats

// Backup copies srcDir to dstDir. See BK.Backup for details.
func Backup(ctx context.Context, srcDir, dstDir string, options BackupOptions) (*BackupStats, error) {
	return BK.Backup(ctx, srcDir, dstDir, options)
}

// Restore copies backupDir to restoreDir. See BK.Restore for details.
func Restore(ctx context.Context, backupDir, restoreDir string) (*RestoreStats, error) {
	return BK.Restore(ctx, backupDir, restoreDir)
}
