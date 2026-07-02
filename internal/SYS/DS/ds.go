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
//
// All connections for the same DSN share a single underlying SY
// engine (see engineCache). Without this sharing, database/sql's
// connection pool would create a fresh engine per Conn and state
// would not be visible across pooled connections.
package DS

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/ST"
	v1 "github.com/cyw0ng95/razordata/internal/SYS/SY"

	// The SE package registers the session constructor into SY via
	// init(). Without this import, Engine.Begin() returns
	// ErrClosed because sessionConstructor is nil.
	_ "github.com/cyw0ng95/razordata/internal/SYS/SE"
)

var registerDriver sync.Once

func init() {
	registerDriver.Do(func() {
		sql.Register("razor", &Driver{})
	})
}

// engineCache is the per-DSN engine cache. database/sql calls
// Open once per connection but expects state (tables, data) to be
// shared across all connections to the same DSN. Each cached engine
// is opened lazily on the first connection and re-closed when the
// driver is no longer reachable (finalizers or explicit Close).
type engineCache struct {
	mu    sync.Mutex
	engs  map[string]*v1.Engine
	refs  map[string]int
	paths map[string]string
}

var globalCache = &engineCache{
	engs:  make(map[string]*v1.Engine),
	refs:  make(map[string]int),
	paths: make(map[string]string),
}

// acquire returns the engine for the given DSN, opening it on the
// first call. The returned engine must be paired with a release.
func (c *engineCache) acquire(ctx context.Context, cfg Config) (*v1.Engine, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if eng, ok := c.engs[cfg.Path]; ok {
		c.refs[cfg.Path]++
		return eng, nil
	}
	opts := AP.Options{InMemory: cfg.Path == ":memory:"}
	eng, err := v1.Open(ctx, cfg.Path, opts)
	if err != nil {
		return nil, err
	}
	c.engs[cfg.Path] = eng
	c.paths[cfg.Path] = cfg.Path
	c.refs[cfg.Path] = 1
	return eng, nil
}

// release decrements the refcount for the DSN. The engine is closed
// when no more connections reference it.
func (c *engineCache) release(cfg Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refs[cfg.Path] > 0 {
		c.refs[cfg.Path]--
	}
	if c.refs[cfg.Path] == 0 {
		if eng, ok := c.engs[cfg.Path]; ok {
			_ = eng.Close(context.Background())
			delete(c.engs, cfg.Path)
			delete(c.refs, cfg.Path)
			delete(c.paths, cfg.Path)
		}
	}
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
	return &Conn{eng: eng, session: sess, cfg: cfg}, nil
}

// Connector implements driver.Connector.
type Connector struct{ cfg Config }

// Connect returns a new connection.
func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	eng, sess, err := openEngine(ctx, c.cfg)
	if err != nil {
		return nil, err
	}
	return &Conn{eng: eng, session: sess, cfg: c.cfg}, nil
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

// openEngine acquires a cached engine for the DSN and opens a new
// session on it. The caller must call releaseEngine on Close.
func openEngine(ctx context.Context, cfg Config) (*v1.Engine, AP.Session, error) {
	eng, err := globalCache.acquire(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	sess, err := eng.Begin(ctx)
	if err != nil {
		globalCache.release(cfg)
		return nil, nil, err
	}
	return eng, sess, nil
}

// releaseEngine releases the cached engine for the DSN.
func releaseEngine(cfg Config) {
	globalCache.release(cfg)
}

// toDriverValue converts a Go value from the executor to a
// database/sql driver.Value. REQ000862: accepts AP.Value directly
// and switches on Kind for zero-alloc conversion.
func toDriverValue(v any) driver.Value {
	if v == nil {
		return nil
	}
	// Fast path: AP.Value — switch on Kind, no boxing.
	if av, ok := v.(AP.Value); ok {
		switch av.Kind {
		case AP.KindNull:
			return nil
		case AP.KindInt:
			return av.I64
		case AP.KindFloat:
			return av.F64
		case AP.KindText:
			return av.S
		case AP.KindBlob:
			return av.B
		case AP.KindBool:
			if av.Bo {
				return int64(1)
			}
			return int64(0)
		default:
			return nil
		}
	}
	// Slow path: raw any from legacy callers.
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
		return nil, AP.New(AP.KindClosed, "engine not open")
	}
	stmt, err := ST.Prepare(conn.eng, query)
	if err != nil {
		return nil, err
	}
	return &Stmt{conn: conn, stmt: stmt}, nil
}
