package EX

import (
	"fmt"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
	"github.com/cyw0ng95/razordata/internal/SQL/RE"
)

type plan struct {
	root    Operator
	params  []string
	cost    float64
	memoKey string
}

type Planner struct {
	mu      sync.Mutex
	memo    map[string]*plan
	catalog map[string]*tableInfo
}

type tableInfo struct {
	name    string
	cols    []ColInfo
	pk      string
	indexes map[string][]string
}

func NewPlanner() *Planner {
	return &Planner{
		memo:    make(map[string]*plan),
		catalog: make(map[string]*tableInfo),
	}
}

func (p *Planner) RegisterTable(name string, cols []ColInfo, pk string) {
	p.catalog[name] = &tableInfo{
		name:    name,
		cols:    cols,
		pk:      pk,
		indexes: make(map[string][]string),
	}
}

func (p *Planner) RegisterIndex(table, index string, cols []string) {
	if t, ok := p.catalog[table]; ok {
		t.indexes[index] = cols
	}
}

func (p *Planner) Plan(stmt PS.Stmt) (*plan, error) {
	key := serializeKey(stmt)
	p.mu.Lock()
	if cached, ok := p.memo[key]; ok {
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	var root Operator

	rewritten, err := RE.Rewrite(stmt)
	if err != nil {
		return nil, err
	}

	switch s := rewritten.(type) {
	case *PS.Select:
		root = p.planSelect(s)
	case *PS.Insert:
		root = p.planInsert(s)
	case *PS.Update:
		root = p.planUpdate(s)
	case *PS.Delete:
		root = p.planDelete(s)
	case *PS.CreateTable:
		root = p.planCreateTable(s)
	case *PS.DropTable:
		root = p.planDropTable(s)
	}

	result := &plan{
		root:    root,
		cost:    p.estimateCost(root),
		memoKey: key,
	}

	p.mu.Lock()
	p.memo[key] = result
	p.mu.Unlock()

	return result, nil
}

func (p *Planner) memoize(key string, plan *plan) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.memo[key] = plan
}

func (p *Planner) estimateCost(op Operator) float64 {
	if op == nil {
		return 0
	}
	return 1.0
}

func (p *Planner) selectIndex(table, col string) (string, bool) {
	t, ok := p.catalog[table]
	if !ok {
		return "", false
	}
	for idxName, idxCols := range t.indexes {
		for _, c := range idxCols {
			if c == col {
				return idxName, true
			}
		}
	}
	return "", false
}

func (p *Planner) planSelect(s *PS.Select) Operator {
	scan := NewSeqScan(s.From)

	var current Operator = scan

	if len(s.Joins) > 0 {
		leftTbl := s.From
		for _, j := range s.Joins {
			if j.Kind != "INNER" && j.Kind != "CROSS" {
				continue
			}
			var on func(outer, inner *Row) (bool, error)
			if j.On != nil {
				pred := j.On
				on = func(outer, inner *Row) (bool, error) {
					v, err := Eval(pred, inner, nil)
					if err != nil {
						return false, err
					}
					return truthy(v), nil
				}
			}
			joinOp := NewNestedLoopJoin(current, NewSeqScan(j.Right), leftTbl, j.Right, on)
			current = joinOp
			leftTbl = j.Right
		}
	}

	if s.Where != nil {
		conjuncts := RE.SplitAnd(s.Where)
		current = NewFilter(scan, conjuncts[0])
		for _, c := range conjuncts[1:] {
			current = NewFilter(current, c)
		}
	}

	needsAggregate := hasAnyAggregate(s.Cols) || len(s.GroupBy) > 0
	groupCols := s.GroupBy
	var aggExprs []PS.Expr
	if needsAggregate {
		aggsOnly, autoGroup, _ := splitSelectCols(s.Cols)
		aggExprs = aggsOnly
		if len(groupCols) == 0 {
			groupCols = autoGroup
		}
		agg := NewAggregate(current, groupCols, aggExprs)
		current = agg
	}

	if s.Having != nil {
		filter := NewFilter(current, s.Having)
		current = filter
	}

	if len(s.Cols) > 0 && !isStarExpr(s.Cols) && !hasAnyAggregate(s.Cols) {
		project := NewProject(current, s.Cols)
		current = project
	}

	if s.Distinct && !hasAnyAggregate(s.Cols) {
		current = NewDistinct(current)
	}

	if len(s.OrderBy) > 0 {
		sort := NewSort(current, s.OrderBy)
		current = sort
	}

	if s.Limit != nil {
		n, ok := limitInt64(s.Limit)
		if !ok {
			return nil
		}
		limit := NewLimit(current, n)
		current = limit
	}

	return current
}

func splitSelectCols(cols []PS.Expr) (aggs, groupCols, other []PS.Expr) {
	if !hasAnyAggregate(cols) {
		return nil, nil, cols
	}
	for _, c := range cols {
		if containsAggregate(c) {
			aggs = append(aggs, c)
			continue
		}
		if _, ok := c.(*PS.StarExpr); ok {
			continue
		}
		groupCols = append(groupCols, c)
	}
	return aggs, groupCols, nil
}

func hasAnyAggregate(cols []PS.Expr) bool {
	for _, c := range cols {
		if containsAggregate(c) {
			return true
		}
	}
	return false
}

func containsAggregate(e PS.Expr) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *PS.AggregateFunc:
		return true
	case *PS.BinaryExpr:
		return containsAggregate(v.Left) || containsAggregate(v.Right)
	case *PS.UnaryExpr:
		return containsAggregate(v.Operand)
	case *PS.AliasedExpr:
		return containsAggregate(v.Expr)
	case *PS.CastExpr:
		return containsAggregate(v.Expr)
	}
	return false
}

func isStarExpr(cols []PS.Expr) bool {
	if len(cols) != 1 {
		return false
	}
	_, ok := cols[0].(*PS.StarExpr)
	return ok
}

func limitInt64(e PS.Expr) (int64, bool) {
	switch v := e.(type) {
	case *PS.NumberLiteral:
		if v.Val < 0 {
			return 0, false
		}
		return v.Val, true
	case *PS.Param:
		_ = v
	}
	return 0, false
}

func (p *Planner) planInsert(s *PS.Insert) Operator {
	return NewInsert(s.Table, s.Cols, s.Values)
}

func (p *Planner) planUpdate(s *PS.Update) Operator {
	scan := NewSeqScan(s.Table)
	return NewUpdate(s.Table, s.Set, s.Where, scan)
}

func (p *Planner) planDelete(s *PS.Delete) Operator {
	scan := NewSeqScan(s.Table)
	return NewDelete(s.Table, s.Where, scan)
}

func (p *Planner) planCreateTable(s *PS.CreateTable) Operator {
	return NewCreateTable(s)
}

func (p *Planner) planDropTable(s *PS.DropTable) Operator {
	return NewDropTable(s)
}

func (p *Planner) ParseAndPlan(sql string) (*plan, error) {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil, fmt.Errorf("pl: parse error: %w", err)
	}
	return p.Plan(stmt)
}
