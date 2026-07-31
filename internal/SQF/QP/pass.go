// Package QP defines the unified logical+physical plan representation
// and a pass-based optimization framework that operates on QueryPlan DAGs.
package QP

// Pass is the unit of QueryPlan optimization. Each pass receives the
// current plan and mutates it in place. A pass that makes no changes
// is a no-op. The optimizer runs passes in order; a non-nil error
// aborts the chain.
type Pass interface {
	Name() string
	Apply(qp *QueryPlan) error
}

// Optimizer holds an ordered list of passes. Optimize runs them
// in order, threading the plan through each one. Zero-pass is a no-op.
type Optimizer struct {
	passes []Pass
}

// NewOptimizer returns an Optimizer with no passes registered.
func NewOptimizer() *Optimizer {
	return &Optimizer{}
}

// AddPass appends a pass to the chain. The order of AddPass calls
// determines execution order.
func (o *Optimizer) AddPass(p Pass) *Optimizer {
	o.passes = append(o.passes, p)
	return o
}

// Optimize runs the registered passes in order.
func (o *Optimizer) Optimize(qp *QueryPlan) error {
	for _, pass := range o.passes {
		if err := pass.Apply(qp); err != nil {
			return err
		}
	}
	return nil
}

// PassCount returns the number of registered passes.
func (o *Optimizer) PassCount() int { return len(o.passes) }

// Passes returns a copy of the registered passes.
func (o *Optimizer) Passes() []Pass {
	out := make([]Pass, len(o.passes))
	copy(out, o.passes)
	return out
}

// --- DAG mutation helpers ---

// replaceNode replaces the node at idx with a new node and returns
// the index of the replacement (which may be len(qp.Nodes) if a
// new node was appended, or an existing index if one was reused).
// Also updates all parent references.
func replaceNode(qp *QueryPlan, idx int, newNode PlanNode) int {
	qp.Nodes[idx] = newNode
	return idx
}

// removeNode removes the node at idx from the plan by replacing
// it with NodeUnknown and removing it from all parent children lists.
// Returns the new root index if the root was removed, or -1.
func removeNode(qp *QueryPlan, idx int) int {
	// Remove from parent children lists
	for i := range qp.Nodes {
		for j, c := range qp.Nodes[i].Children {
			if c == idx {
				qp.Nodes[i].Children = append(
					qp.Nodes[i].Children[:j],
					qp.Nodes[i].Children[j+1:]...,
				)
				break
			}
		}
	}
	// If this was the root, return -1 to signal removal
	if idx == qp.RootIdx {
		qp.RootIdx = -1
		return -1
	}
	return idx
}

// walkBottomUp calls fn on each node in depth-first post-order.
// fn receives the node index and a pointer to the node.
// If fn returns false, traversal of that subtree is skipped
// (node and its descendants are not visited).
func (qp *QueryPlan) walkBottomUp(fn func(idx int, node *PlanNode) bool) {
	walkBU(qp.RootIdx, qp.Nodes, fn)
}

func walkBU(idx int, nodes []PlanNode, fn func(idx int, node *PlanNode) bool) {
	node := &nodes[idx]
	for _, childIdx := range node.Children {
		walkBU(childIdx, nodes, fn)
	}
	if !fn(idx, node) {
		return
	}
}

// findNodeIdx returns the index of n in qp.Nodes, or -1 if not found.
func (qp *QueryPlan) findNodeIdx(n *PlanNode) int {
	if n == nil {
		return -1
	}
	for i := range qp.Nodes {
		if &qp.Nodes[i] == n {
			return i
		}
	}
	return -1
}
