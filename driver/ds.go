// Package driver implements the database/sql driver for razordata.
// Import this package to register the "razor" driver with Go's
// standard database/sql package.
//
// DSN format:
//
//	:memory:                  in-memory database
//	./path/to/db.razor         file-backed database
//	/path/to/db.razor          file-backed database
package driver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"os"
	"strings"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/ST"
	v1 "github.com/cyw0ng95/razordata/internal/SYS/SY"

	_ "github.com/cyw0ng95/razordata/internal/SYS/SE"
)

func init() {
	sql.Register("razor", &Driver{})
}

// engineCache holds one engine per DSN. The key is the resolved
// filesystem path (or ":memory:"). Multiple database/sql connections
// share the same engine; each connection only creates a new session.
var (
	engineMu  sync.Mutex
	engines   = map[string]*v1.Engine{}
	dirByDSN  = map[string]string{}
)

// Config is the parsed DSN.
type Config struct {
	Path string
}

// Driver implements driver.Driver.
type Driver struct{}

// Open returns a new database/sql connection backed by razordata.
func (d *Driver) Open(name string) (driver.Conn, error) {
	cfg := parseDSN(name)
	return d.openConn(context.Background(), cfg)
}

// OpenConnector implements driver.DriverContext.
func (d *Driver) OpenConnector(name string) (driver.Connector, error) {
	cfg := parseDSN(name)
	return &Connector{cfg: cfg}, nil
}

func (d *Driver) openConn(ctx context.Context, cfg Config) (driver.Conn, error) {
	eng, _, err := getOrCreateEngine(ctx, cfg)
	if err != nil {
		return nil, err
	}
	sess, err := eng.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &Conn{eng: eng, session: sess}, nil
}

// Connector implements driver.Connector.
type Connector struct{ cfg Config }

// Connect returns a new connection.
func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	eng, _, err := getOrCreateEngine(ctx, c.cfg)
	if err != nil {
		return nil, err
	}
	sess, err := eng.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &Conn{eng: eng, session: sess}, nil
}

func (c *Connector) Driver() driver.Driver { return &Driver{} }

func parseDSN(name string) Config {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, ":memory:") {
		return Config{Path: ":memory:"}
	}
	return Config{Path: name}
}

// getOrCreateEngine returns a cached engine for the DSN, creating
// one if it doesn't exist yet. For ":memory:", a fresh engine is
// created every time. For file-backed databases, the engine is
// reused across connections so data persists.
func getOrCreateEngine(ctx context.Context, cfg Config) (*v1.Engine, string, error) {
	engineMu.Lock()
	defer engineMu.Unlock()

	key := cfg.Path

	// For file-backed databases, reuse the existing engine.
	if key != ":memory:" {
		if eng, ok := engines[key]; ok {
			return eng, dirByDSN[key], nil
		}
	}

	// Clear EX state before opening a new engine.
	EX.UnregisterAll()

	// For file-backed databases, use the parent directory.
	dir := key
	if key != ":memory:" {
		var err error
		dir, err = createTempDirFor(key)
		if err != nil {
			return nil, "", err
		}
	}

	opts := AP.Options{InMemory: key == ":memory:"}
	eng, err := v1.Open(ctx, dir, opts)
	if err != nil {
		return nil, "", err
	}

	engines[key] = eng
	dirByDSN[key] = dir
	return eng, dir, nil
}

// createTempDirFor creates a temp directory for the database.
func createTempDirFor(dsn string) (string, error) {
	dir := dsn + ".engine"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// CloseEngine removes a cached engine. Called by Conn.Close when
// the database/sql pool is fully drained.
func CloseEngine(dsn string) {
	engineMu.Lock()
	defer engineMu.Unlock()
	delete(engines, dsn)
	delete(dirByDSN, dsn)
}

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

// Prepare is exported for tests that need direct statement preparation.
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
