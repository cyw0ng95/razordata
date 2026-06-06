package EX

import (
	"context"
	"errors"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

var ErrNotImplemented = errors.New("ex: not implemented")
var ErrNoRows = errors.New("ex: no rows")
var ErrClosed = errors.New("ex: operator closed")

type Operator interface {
	Next(ctx context.Context) (Row, error)
	Close() error
}

type Row struct {
	Cols  []string
	Types []int
	Data  []interface{}
	Outer *Row
}

func (r *Row) Lookup(name string) (interface{}, bool) {
	for cur := r; cur != nil; cur = cur.Outer {
		for i, c := range cur.Cols {
			if c == name {
				if i < len(cur.Data) {
					return cur.Data[i], true
				}
				return nil, false
			}
		}
	}
	return nil, false
}

type Result struct {
	RowsAffected int64
	LastInsertID uint64
}

type Rows struct {
	Cols  []string
	Types []int
}

type ColInfo struct {
	Name     string
	Typ      int
	Nullable bool          // default true; false means NOT NULL
	Default  PS.Expr       // nil means no DEFAULT clause
	PK       bool          // true means primary key (implies NOT NULL)
}

type Executor struct {
	planner *Planner
	store   Store
	// txWriter, when non-nil, is notified of every key the executor
	// writes (Insert/Update/Delete) so a higher-level transaction
	// layer can capture a shadow writeSet for ROLLBACK. SetTxWriter
	// and ClearTxWriter toggle it; reads are unaffected.
	txWriter TxWriter
}

// TxWriter is the optional hook an Executor notifies on every key
// write. Implementations record the pre-write value so ROLLBACK can
// restore. The SYS layer wires this for transactional sessions.
type TxWriter interface {
	RecordWrite(key []byte, newValue []byte)
}

// SetTxWriter installs w as the current transaction's write hook. Pass
// nil to disable. Not safe to call concurrently with Exec; the
// caller (a Session) is responsible for serialization.
func (e *Executor) SetTxWriter(w TxWriter) { e.txWriter = w }

// ClearTxWriter resets the write hook to nil. Pair with SetTxWriter.
func (e *Executor) ClearTxWriter() { e.txWriter = nil }

func NewExecutor() *Executor {
	return &Executor{planner: NewPlanner()}
}

func NewExecutorWithPlanner(pl *Planner) *Executor {
	return &Executor{planner: pl}
}

// NewExecutorWithEngine wires the executor to a real storage engine. When
// store is non-nil, SeqScan / Insert / Update / Delete route through it
// instead of the in-memory tables map. Pass nil to revert to in-memory
// mode.
func NewExecutorWithEngine(store Store) *Executor {
	return &Executor{planner: NewPlannerWithStore(store), store: store}
}

func (e *Executor) RegisterTable(name string, schema []string) {
	cols := make([]ColInfo, len(schema))
	for i, n := range schema {
		cols[i] = ColInfo{Name: n, Typ: 1}
	}
	e.planner.RegisterTable(name, cols, "")
	RegisterTableSchema(name, schema)
	if e.store != nil {
		registerStoreSchema(name, schema, "")
	}
}

func (e *Executor) RegisterTableWithPK(name string, schema []string, pk string) {
	cols := make([]ColInfo, len(schema))
	for i, n := range schema {
		cols[i] = ColInfo{Name: n, Typ: 1}
	}
	e.planner.RegisterTable(name, cols, pk)
	RegisterTableSchema(name, schema)
	if e.store != nil {
		registerStoreSchema(name, schema, pk)
	}
}

func (e *Executor) RegisterIndex(table, index string, cols []string) {
	e.planner.RegisterIndex(table, index, cols)
}

func (e *Executor) Exec(ctx context.Context, sql string, args ...any) (Result, error) {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return Result{}, err
	}
	op, err := e.buildWriterOp(stmt)
	if err != nil {
		return Result{}, err
	}
	defer op.Close()
	if _, err := op.Next(ctx); err != nil && err != ErrNoRows {
		return Result{}, err
	}
	return extractResult(op)
}

func (e *Executor) Query(ctx context.Context, sql string, args ...any) (*Rows, error) {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil, err
	}
	plan, err := e.planner.Plan(stmt)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.root == nil {
		return nil, errors.New("ex: plan produced no root")
	}
	defer plan.root.Close()
	row, err := plan.root.Next(ctx)
	if err != nil {
		if err == ErrNoRows {
			return &Rows{}, nil
		}
		return nil, err
	}
	rs := &Rows{Cols: append([]string(nil), row.Cols...), Types: append([]int(nil), row.Types...)}
	return rs, nil
}

func (e *Executor) QueryAll(ctx context.Context, sql string, args ...any) ([]Row, error) {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil, err
	}
	plan, err := e.planner.Plan(stmt)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.root == nil {
		return nil, errors.New("ex: plan produced no root")
	}
	defer plan.root.Close()
	var out []Row
	for {
		row, err := plan.root.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// Explain plans the statement and returns a human-readable
// description of the operator tree. The plan is closed before
// returning, so Explain does not run the query.
func (e *Executor) Explain(sql string) (string, error) {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return "", err
	}
	plan, err := e.planner.Plan(stmt)
	if err != nil {
		return "", err
	}
	if plan == nil || plan.root == nil {
		return "", errors.New("ex: plan produced no root")
	}
	defer plan.root.Close()
	return explainOperator(plan.root, 0), nil
}

func (e *Executor) buildWriterOp(stmt PS.Stmt) (Operator, error) {
	switch s := stmt.(type) {
	case *PS.Insert:
		if e.store != nil {
			op, err := NewInsertWithStore(e.store, s.Table, s.Cols, s.Values)
			if err != nil {
				return nil, err
			}
			return op, nil
		}
		return NewInsert(s.Table, s.Cols, s.Values), nil
	case *PS.Update:
		var scan Operator = NewSeqScan(s.Table)
		if e.store != nil {
			ssc, err := NewSeqScanWithStore(e.store, s.Table)
			if err != nil {
				return nil, err
			}
			scan = ssc
		}
		filter := NewFilter(scan, s.Where)
		if e.store != nil {
			op, err := NewUpdateWithStore(e.store, s.Table, s.Set, s.Where, filter)
			if err != nil {
				return nil, err
			}
			return op, nil
		}
		return NewUpdate(s.Table, s.Set, s.Where, filter), nil
	case *PS.Delete:
		var scan Operator = NewSeqScan(s.Table)
		if e.store != nil {
			ssc, err := NewSeqScanWithStore(e.store, s.Table)
			if err != nil {
				return nil, err
			}
			scan = ssc
		}
		filter := NewFilter(scan, s.Where)
		if e.store != nil {
			op, err := NewDeleteWithStore(e.store, s.Table, s.Where, filter)
			if err != nil {
				return nil, err
			}
			return op, nil
		}
		return NewDelete(s.Table, s.Where, filter), nil
	case *PS.CreateTable:
		return NewCreateTable(s), nil
	case *PS.DropTable:
		return NewDropTable(s), nil
	}
	return nil, errors.New("ex: not a writable statement")
}

func extractResult(op Operator) (Result, error) {
	type affected interface {
		RowsAffected() int64
	}
	if a, ok := op.(affected); ok {
		return Result{RowsAffected: a.RowsAffected()}, nil
	}
	return Result{}, nil
}
