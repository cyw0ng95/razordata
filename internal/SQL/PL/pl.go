package PL

import (
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// Rows is a forward-declared prepared-statement result descriptor.
type Rows struct {
	Cols  []string
	Types []int
}

// Result is a forward-declared prepared-statement result.
type Result struct {
	RowsAffected int64
	LastInsertID uint64
}

// Stmt is the public prepared-statement surface PL exposes. The concrete
// implementation is built by EX and returned from Executor.Prepare.
type Stmt interface {
	Query(args ...any) (*Rows, error)
	Exec(args ...any) (Result, error)
	Close() error
}

// PlanOptions are inputs to PlanStmt (the cluster's tree-construction
// entry point). The EX planner implements this against its own operator
// constructors and returns the resulting plan with the memoized key.
type PlanOptions struct {
	// Executor-provided hook that returns a memoized plan. PL just owns
	// the cache; EX owns the tree construction.
	BuildTree func(stmt PS.Stmt) (cost float64, err error)
}

// Planner is the public PL-side planner entry point. It owns a Memo
// and delegates tree construction to the registered BuildTree hook.
type Planner struct {
	memo *Memo
	opts PlanOptions
}

// NewPlanner returns a planner with an empty memo and a no-op build hook.
func NewPlanner() *Planner {
	return &Planner{memo: NewMemo(), opts: PlanOptions{BuildTree: func(PS.Stmt) (float64, error) { return 0, nil }}}
}

// NewPlannerWith builds a planner with the given options.
func NewPlannerWith(opts PlanOptions) *Planner {
	if opts.BuildTree == nil {
		opts.BuildTree = func(PS.Stmt) (float64, error) { return 0, nil }
	}
	return &Planner{memo: NewMemo(), opts: opts}
}

// Memo exposes the underlying memo for inspection.
func (p *Planner) Memo() *Memo { return p.memo }

// Plan runs the planner: compute a key, hit the memo, otherwise call
// the BuildTree hook and cache the result.
func (p *Planner) Plan(stmt PS.Stmt) (Plan, error) {
	key := SerializeKeyWithSchema(stmt, p.memo.SchemaVersion())
	if cached, ok := p.memo.Get(key); ok {
		return cached, nil
	}
	cost, err := p.opts.BuildTree(stmt)
	if err != nil {
		return Plan{}, err
	}
	pl := Plan{Cost: cost, MemoKey: key}
	p.memo.Put(key, pl)
	return pl, nil
}
