// Package OC provides the optimization-core types and the Optimizer
// pipeline for razordata. It is the only package SQO exposes to
// SQB/EX — concrete passes (PF, CO, JO, RS) live in their own
// packages and register themselves with the Optimizer at construction
// time.
//
// REQ001432: empty Optimizer scaffold. Passes are added in later
// iterations (REQ001443–449). Until then, Optimize() returns the
// input plan unchanged, so the SQF → SQO → SQB dependency order
// is established without changing any behavior.
package OC

import (
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// Plan is the optimizer's view of a query plan. The root is a
// generic pl.Operator so the optimizer does not depend on SQB/OP
// concrete types.
type Plan struct {
	Root pl.Operator
	// Stmt is the originating SQL statement, kept here so passes
	// (e.g. SubqueryDecorrelation) can reach the AST without
	// re-parsing. May be nil if the plan was built programmatically.
	Stmt PS.Stmt
}

// Context carries the side-tables a pass needs: catalog, stats,
// per-table schema. It is the only place SQO touches the storage
// engine — through the CatalogReader and StatsReader interfaces.
type Context struct {
	Catalog CatalogReader
	Stats   StatsReader
	// Tables exposes per-table schema (column list, PK) for passes
	// that need to know which columns exist. The concrete value is
	// supplied by SQB/EX; SQO does not import SQB.
	Tables map[string]TableSchema
}

// TableSchema is the column/PK info a pass needs to make
// rewrites. Full schema (types, defaults, indexes) lives in the
// catalog; this is the minimum surface.
type TableSchema struct {
	Columns []string
	PK      string
}

// Pass is the unit of optimization. Each pass receives the current
// plan and a context, and returns a (possibly rewritten) plan. A
// pass that returns the input unchanged is a no-op.
type Pass interface {
	Name() string
	Apply(p *Plan, ctx *Context) (*Plan, error)
}

// Optimizer holds an ordered list of passes. Optimize runs them
// in order, threading the plan through each one. The current
// scaffold runs zero passes; REQ001450 wires the full pass chain.
type Optimizer struct {
	passes []Pass
}

// New returns an Optimizer with no passes registered.
func New() *Optimizer {
	return &Optimizer{}
}

// AddPass appends a pass to the chain. The order of AddPass calls
// determines execution order.
func (o *Optimizer) AddPass(p Pass) *Optimizer {
	o.passes = append(o.passes, p)
	return o
}

// Optimize runs the registered passes in order. With no passes
// registered (REQ001432 scaffold), it returns the input plan
// unchanged — the empty-optimizer behavior that lets SQB/EX
// adopt SQO/OC without changing plan output.
func (o *Optimizer) Optimize(p *Plan, ctx *Context) (*Plan, error) {
	cur := p
	for _, pass := range o.passes {
		next, err := pass.Apply(cur, ctx)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

// PassCount returns the number of registered passes. Used by tests
// and REQ001450 to assert pass-chain wiring.
func (o *Optimizer) PassCount() int { return len(o.passes) }

// Passes returns the registered passes (read-only). Used by tests
// and by the EXPLAIN integration to dump the pass chain.
func (o *Optimizer) Passes() []Pass {
	out := make([]Pass, len(o.passes))
	copy(out, o.passes)
	return out
}
