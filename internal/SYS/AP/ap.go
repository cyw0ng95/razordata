package AP

import (
	"context"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SYS/BK"
)

const Version = "0.9.0"

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
	EmergencyShutdown  bool          // REQ000688: skip Phase 2+3, flush only
	MaxMemoryPerQuery  int64         // REQ001056: per-query memory cap, 0 = unlimited
	JoinBufferSize     int64         // REQ001056: per-hash-join memory cap, 0 = unlimited
	MaxResultRows      int64         // REQ001056: per-query result row cap, 0 = unlimited
}

const (
	DefaultPageSize        = 4096
	DefaultMemTableSize    = 64 * 1024 * 1024
	DefaultBufferPoolMB    = 256
	DefaultWALSizeMB       = 64
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

func (r *Rows) Cols() []string { return r.cols }
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

var (
	ErrNoActiveTxn      = New(KindConstraint, "no active transaction")
	ErrUnknownSavepoint = New(KindConstraint, "unknown savepoint")
	ErrConstraint       = New(KindConstraint, "constraint violation")
)

func (r *Row) Len() int { return len(r.Cols) }

// Value kind constants for the tagged-union Value type (REQ000776/REQ000862).
const (
	KindNull ValueKind = iota
	KindInt
	KindFloat
	KindText
	KindBlob
	KindBool
)

// ValueKind is the type discriminator for Value.
type ValueKind uint8

// String returns a human-readable representation of the ValueKind.
func (k ValueKind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindInt:
		return "int64"
	case KindFloat:
		return "float64"
	case KindText:
		return "string"
	case KindBlob:
		return "blob"
	case KindBool:
		return "bool"
	default:
		return "unknown"
	}
}

// Value is a tagged-union that stores SQL values inline without boxing.
// The zero value (Kind=0, all fields zero) represents SQL NULL.
// REQ000862: defined in SYS/AP so both EX (executor) and the driver
// layer can use the same concrete type without []any boxing.
type Value struct {
	Kind ValueKind
	I64  int64
	F64  float64
	S    string
	B    []byte
	Bo   bool
}

// NewIntValue creates a Value from an int64.
func NewIntValue(v int64) Value { return Value{Kind: KindInt, I64: v} }

// NewFloatValue creates a Value from a float64.
func NewFloatValue(v float64) Value { return Value{Kind: KindFloat, F64: v} }

// NewTextValue creates a Value from a string.
func NewTextValue(v string) Value { return Value{Kind: KindText, S: v} }

// NewBlobValue creates a Value from a byte slice.
func NewBlobValue(v []byte) Value { return Value{Kind: KindBlob, B: v} }

// NewBoolValue creates a Value from a bool.
func NewBoolValue(v bool) Value { return Value{Kind: KindBool, Bo: v} }

// NullValue returns a NULL Value.
func NullValue() Value { return Value{Kind: KindNull} }

// IsNull returns true if this Value represents SQL NULL.
func (v Value) IsNull() bool { return v.Kind == KindNull }

// AsInt returns the int64 value (0 if not int).
func (v Value) AsInt() int64 { return v.I64 }

// AsFloat returns the float64 value (0 if not float).
func (v Value) AsFloat() float64 { return v.F64 }

// AsString returns the string value ("" if not text).
func (v Value) AsString() string { return v.S }

// AsBlob returns the []byte value (nil if not blob).
func (v Value) AsBlob() []byte { return v.B }

// AsBool returns the bool value (false if not bool).
func (v Value) AsBool() bool { return v.Bo }

// ToAny converts a Value to the boxed any representation.
func (v Value) ToAny() any {
	switch v.Kind {
	case KindNull:
		return nil
	case KindInt:
		return v.I64
	case KindFloat:
		return v.F64
	case KindText:
		return v.S
	case KindBlob:
		return v.B
	case KindBool:
		return v.Bo
	default:
		return nil
	}
}

// String returns the string representation of a Value without boxing
// through any or fmt.Sprint. REQ001066: avoids reflection overhead
// in hot-path functions like SUBSTR, CONCAT, and CAST.
func (v Value) String() string {
	switch v.Kind {
	case KindText:
		return v.S
	case KindInt:
		return strconv.FormatInt(v.I64, 10)
	case KindFloat:
		return strconv.FormatFloat(v.F64, 'g', -1, 64)
	case KindBool:
		if v.Bo {
			return "1"
		}
		return "0"
	case KindNull:
		return ""
	default:
		return ""
	}
}

// Equal compares two Values for equality. Two NULLs are equal.
func (v Value) Equal(other Value) bool {
	if v.Kind != other.Kind {
		return false
	}
	switch v.Kind {
	case KindNull:
		return true
	case KindInt:
		return v.I64 == other.I64
	case KindFloat:
		return v.F64 == other.F64
	case KindText:
		return v.S == other.S
	case KindBlob:
		if len(v.B) != len(other.B) {
			return false
		}
		for i := range v.B {
			if v.B[i] != other.B[i] {
				return false
			}
		}
		return true
	case KindBool:
		return v.Bo == other.Bo
	}
	return false
}

type BackupOptions = BK.BackupOptions
type BackupStats = BK.BackupStats
type RestoreStats = BK.RestoreStats

func Backup(ctx context.Context, srcDir, dstDir string, options BackupOptions) (*BackupStats, error) {
	return BK.Backup(ctx, srcDir, dstDir, options)
}

func Restore(ctx context.Context, backupDir, restoreDir string) (*RestoreStats, error) {
	return BK.Restore(ctx, backupDir, restoreDir)
}
