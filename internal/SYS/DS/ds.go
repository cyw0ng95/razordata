// Package DS implements the database/sql driver for razordata. The
// driver is registered with Go's standard database/sql package on
// import via init(). DSN format:
//
//	:memory:                  in-memory database
//	./path/to/db.razor         file-backed database
//	/path/to/db.razor          file-backed database
//
// The driver implements driver.Driver, driver.Connector, and the
// Conn/Stmt/Rows/Tx interfaces required by database/sql.
package DS

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/ST"
	v1 "github.com/cyw0ng95/razordata/internal/SYS/SY"

	// The SE package registers the session constructor into SY via
	// init(). Without this import, Engine.Begin() returns
	// ErrClosed because sessionConstructor is nil.
	_ "github.com/cyw0ng95/razordata/internal/SYS/SE"
)

func init() {
	sql.Register("razor", &Driver{})
}

// Config is the parsed DSN.
type Config struct {
	// Path is the database path. ":memory:" for in-memory mode,
	// otherwise a file system path.
	Path string
}

// Driver implements driver.Driver.
type Driver struct{}

// Open returns a new database/sql connection backed by an in-memory
// or file-based razordata engine. Implements driver.Driver.
func (d *Driver) Open(name string) (driver.Conn, error) {
	cfg := parseDSN(name)
	return d.openConn(context.Background(), cfg)
}

// OpenConnector implements driver.DriverContext (Go 1.10+).
func (d *Driver) OpenConnector(name string) (*Connector, error) {
	cfg := parseDSN(name)
	return &Connector{cfg: cfg}, nil
}

func (d *Driver) openConn(ctx context.Context, cfg Config) (driver.Conn, error) {
	eng, sess, err := openEngine(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &Conn{eng: eng, session: sess}, nil
}

// Connector implements driver.Connector.
type Connector struct{ cfg Config }

// Connect returns a new connection.
func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	eng, sess, err := openEngine(ctx, c.cfg)
	if err != nil {
		return nil, err
	}
	return &Conn{eng: eng, session: sess}, nil
}

// Driver returns the underlying driver.
func (c *Connector) Driver() driver.Driver { return &Driver{} }

// parseDSN converts a database/sql DSN string to a Config. The
// supported DSNs are ":memory:" (in-memory) and a file path
// (file-backed). Any other empty/whitespace string defaults to
// in-memory mode.
func parseDSN(name string) Config {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, ":memory:") {
		return Config{Path: ":memory:"}
	}
	return Config{Path: name}
}

// openEngine constructs a new SY engine and returns it together
// with an active session. Caller is responsible for closing the
// engine.
func openEngine(ctx context.Context, cfg Config) (*v1.Engine, AP.Session, error) {
	opts := AP.Options{InMemory: cfg.Path == ":memory:"}
	eng, err := v1.Open(ctx, cfg.Path, opts)
	if err != nil {
		return nil, nil, err
	}
	sess, err := eng.Begin(ctx)
	if err != nil {
		eng.Close(ctx)
		return nil, nil, err
	}
	return eng, sess, nil
}

// toDriverValue converts a Go value from the executor to a
// database/sql driver.Value. The conversion is lossy: bools become
// int64 (0/1) to match SQLite's convention.
func toDriverValue(v any) driver.Value {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return x
	case string:
		return x
	case []byte:
		return x
	case bool:
		if x {
			return int64(1)
		}
		return int64(0)
	default:
		return nil
	}
}

// Prepare implements driver.Conn by delegating to ST.Prepare. The
// returned driver.Stmt owns a *ST.Stmt bound to the connection.
func Prepare(conn *Conn, query string) (driver.Stmt, error) {
	if conn == nil || conn.eng == nil {
		return nil, AP.ErrNotOpen
	}
	stmt, err := ST.Prepare(conn.eng, query)
	if err != nil {
		return nil, err
	}
	return &Stmt{conn: conn, stmt: stmt}, nil
}
