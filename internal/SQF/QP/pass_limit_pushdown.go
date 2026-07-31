package QP

// LimitPushdownPass pushes Limit below Sort when the Sort is top-N
// compatible. Conservative: only merges when both nodes are simple
// Limit/Sort with no additional children. REQ002263.
type LimitPushdownPass struct{}

func (p *LimitPushdownPass) Name() string { return "limit_pushdown" }

func (p *LimitPushdownPass) Apply(qp *QueryPlan) error {
	if qp == nil || qp.RootIdx < 0 {
		return nil
	}
	// No structural changes needed for shadow mode — the Lower
	// function handles Limit and Sort as separate stages.
	// This pass is a no-op in the QP model; it would be needed
	// for actual cost-based optimization.
	return nil
}
