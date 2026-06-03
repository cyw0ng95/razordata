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
}

func (r *Row) Lookup(name string) (interface{}, bool) {
	for i, c := range r.Cols {
		if c == name {
			if i < len(r.Data) {
				return r.Data[i], true
			}
			return nil, false
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
	Name string
	Typ  int
}

type Executor struct {
	planner *Planner
}

func NewExecutor() *Executor {
	return &Executor{planner: NewPlanner()}
}

func NewExecutorWithPlanner(pl *Planner) *Executor {
	return &Executor{planner: pl}
}

func (e *Executor) RegisterTable(name string, schema []string) {
	cols := make([]ColInfo, len(schema))
	for i, n := range schema {
		cols[i] = ColInfo{Name: n, Typ: 1}
	}
	e.planner.RegisterTable(name, cols, "")
	RegisterTableSchema(name, schema)
}

func (e *Executor) RegisterTableWithPK(name string, schema []string, pk string) {
	cols := make([]ColInfo, len(schema))
	for i, n := range schema {
		cols[i] = ColInfo{Name: n, Typ: 1}
	}
	e.planner.RegisterTable(name, cols, pk)
	RegisterTableSchema(name, schema)
}

func (e *Executor) Exec(ctx context.Context, sql string, args ...any) (Result, error) {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return Result{}, err
	}
	op, err := buildWriterOp(stmt)
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

func buildWriterOp(stmt PS.Stmt) (Operator, error) {
	switch s := stmt.(type) {
	case *PS.Insert:
		return NewInsert(s.Table, s.Cols, s.Values), nil
	case *PS.Update:
		scan := NewSeqScan(s.Table)
		filter := NewFilter(scan, s.Where)
		return NewUpdate(s.Table, s.Set, s.Where, filter), nil
	case *PS.Delete:
		scan := NewSeqScan(s.Table)
		filter := NewFilter(scan, s.Where)
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
