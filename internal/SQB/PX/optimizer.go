package PX

// Optimizer holds an ordered list of physical passes. Optimize runs
// them sequentially over a *PipelineSpec. The current scaffold runs
// zero passes; passes are registered at build time and exercised by
// future iterations (REQ002225 ScanFusion pass, etc.).
//
// Mirrors SQO/OC.Optimizer (optimizer.go) at the logical level.
type Optimizer struct {
	passes []Pass
}

// New returns an Optimizer with no passes registered.
func NewOptimizer() *Optimizer {
	return &Optimizer{}
}

// AddPass appends a pass to the chain. The order of AddPass calls
// determines execution order.
func (o *Optimizer) AddPass(p Pass) *Optimizer {
	o.passes = append(o.passes, p)
	return o
}

// Optimize runs the registered passes in order over the given spec.
// With no passes registered it is a no-op. Each pass receives a fresh
// PassContext with its own Rewriter; all pass commits are effective
// before the next pass begins (Commit is called per-pass, not batched).
func (o *Optimizer) Optimize(spec *PipelineSpec) error {
	for _, pass := range o.passes {
		ctx := &PassContext{
			Rewriter: NewRewriter(spec),
		}
		if err := pass.Apply(ctx, spec); err != nil {
			return err
		}
		if err := ctx.Rewriter.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// PassCount returns the number of registered passes. Used by tests
// and the EXPLAIN integration to assert pass-chain wiring.
func (o *Optimizer) PassCount() int { return len(o.passes) }
