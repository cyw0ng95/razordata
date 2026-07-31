package PX

// Pass is the unit of physical optimization. Each pass receives a
// PassContext and a *PipelineSpec, and may mutate the spec in place.
// A pass that returns the input unchanged is a no-op. Passes are
// applied sequentially by the Optimizer; an error from any pass
// aborts the chain and propagates to the caller.
//
// Mirrors SQO/OC.Pass (optimizer.go) at the logical level.
type Pass interface {
	Name() string
	Apply(ctx *PassContext, spec *PipelineSpec) error
}

// PassContext carries the side-tables a pass needs, including a
// Rewriter for DAG mutations. It is created fresh for each spec
// optimization so passes do not share mutable state.
type PassContext struct {
	Rewriter *Rewriter
}
