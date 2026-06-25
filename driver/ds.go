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

func init() { sql.Register("razor", &Driver{}) }

var (
	engineMu sync.Mutex
	engines  = map[string]*v1.Engine{}
	dirByDSN = map[string]string{}
)

type Config struct{ Path string }

type Driver struct{}

func (d *Driver) Open(name string) (driver.Conn, error) {
	cfg := parseDSN(name)
	return d.openConn(context.Background(), cfg)
}

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

type Connector struct{ cfg Config }

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

func getOrCreateEngine(ctx context.Context, cfg Config) (*v1.Engine, string, error) {
	engineMu.Lock()
	defer engineMu.Unlock()
	key := cfg.Path
	if key != ":memory:" {
		if eng, ok := engines[key]; ok {
			if eng.IsClosed() {
				delete(engines, key)
				delete(dirByDSN, key)
			} else {
				return eng, dirByDSN[key], nil
			}
		}
	}
	EX.UnregisterAll()
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

func createTempDirFor(dsn string) (string, error) {
	dir := dsn + ".engine"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func CloseEngine(dsn string) {
	engineMu.Lock()
	defer engineMu.Unlock()
	if eng, ok := engines[dsn]; ok {
		_ = eng.Close(context.Background())
	}
	delete(engines, dsn)
	delete(dirByDSN, dsn)
}

// RegisterEngine pre-registers a caller-created engine under the
// given DSN so that a subsequent sql.Open("razor", dsn) reuses it
// instead of creating a new one with default options.
func RegisterEngine(dsn string, eng *v1.Engine) {
	engineMu.Lock()
	defer engineMu.Unlock()
	engines[dsn] = eng
	dirByDSN[dsn] = ""
}

// toDriverValue converts a value to database/sql driver.Value.
// REQ000867: fast path for AP.Value (Kind switch, no boxing).
// Slow path for raw any from driver.Value or legacy callers.
func toDriverValue(v any) driver.Value {
	if v == nil {
		return nil
	}
	// REQ000867: AP.Value fast path — switch on Kind constant,
	// no pointer indirection, no runtime type lookup.
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
	// Slow path: raw any from driver.Value (stmt.go params).
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
