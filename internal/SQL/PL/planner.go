package PL

import (
	"fmt"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type plan struct {
	root    EX.Operator
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
	cols    []colInfo
	pk      string
	indexes map[string][]string
}

type colInfo struct {
	name string
	typ  int
}

func NewPlanner() *Planner {
	return &Planner{
		memo:    make(map[string]*plan),
		catalog: make(map[string]*tableInfo),
	}
}

func (p *Planner) RegisterTable(name string, cols []colInfo, pk string) {
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

	var root EX.Operator

	switch s := stmt.(type) {
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

func (p *Planner) estimateCost(op EX.Operator) float64 {
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

func (p *Planner) planSelect(s *PS.Select) EX.Operator {
	scan := EX.NewSeqScan(s.From, s.Where)

	var current EX.Operator = scan

	if s.Where != nil {
		filter := EX.NewFilter(scan, s.Where)
		current = filter
	}

	if len(s.Cols) > 0 && !isStarExpr(s.Cols) {
		project := EX.NewProject(current, s.Cols)
		current = project
	}

	if len(s.OrderBy) > 0 {
		sort := EX.NewSort(current, s.OrderBy)
		current = sort
	}

	if s.Limit != nil {
		limit := EX.NewLimit(current, s.Limit)
		current = limit
	}

	return current
}

func isStarExpr(cols []PS.Expr) bool {
	if len(cols) != 1 {
		return false
	}
	_, ok := cols[0].(*PS.StarExpr)
	return ok
}

func (p *Planner) planInsert(s *PS.Insert) EX.Operator {
	return EX.NewInsert(s.Table, s.Cols, s.Values)
}

func (p *Planner) planUpdate(s *PS.Update) EX.Operator {
	scan := EX.NewSeqScan(s.Table, s.Where)
	return EX.NewUpdate(s.Table, s.Set, s.Where, scan)
}

func (p *Planner) planDelete(s *PS.Delete) EX.Operator {
	scan := EX.NewSeqScan(s.Table, s.Where)
	return EX.NewDelete(s.Table, s.Where, scan)
}

func (p *Planner) planCreateTable(s *PS.CreateTable) EX.Operator {
	return EX.NewCreateTable(s)
}

func (p *Planner) planDropTable(s *PS.DropTable) EX.Operator {
	return EX.NewDropTable(s)
}

func (p *Planner) ParseAndPlan(sql string) (*plan, error) {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil, fmt.Errorf("pl: parse error: %w", err)
	}
	return p.Plan(stmt)
}
