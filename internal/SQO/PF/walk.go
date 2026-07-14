// Package PF provides optimizer passes that rewrite the operator
// tree using only pl interfaces — never importing SQB concrete types.
// Each pass implements OC.Pass and is registered with the Optimizer.
package PF

import (
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// WalkOp recursively walks the operator tree depth-first, calling fn
// on each node. fn may return the same node or a replacement. WalkOp
// recurses into children by checking pl.Children2 then pl.Parent.
//
// Safe for passes that need to inspect or rewrite every node.
func WalkOp(root pl.Operator, fn func(pl.Operator) pl.Operator) pl.Operator {
	if root == nil {
		return nil
	}
	root = fn(root)
	if root == nil {
		return nil
	}
	if c2, ok := root.(pl.Children2); ok {
		left := WalkOp(c2.Left(), fn)
		c2.SetLeft(left)
		right := WalkOp(c2.Right(), fn)
		c2.SetRight(right)
	} else if p, ok := root.(pl.Parent); ok {
		child := WalkOp(p.Child(), fn)
		p.SetChild(child)
	}
	return root
}