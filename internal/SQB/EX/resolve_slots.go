package EX

import (
	"strings"

	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// ResolvePlanSlots walks the operator tree and resolves Ident/QualifiedName
// SlotIdx values to direct row.Data indices. After this pass, column reference
// evaluation skips the runtime ColIndex map build + hash lookup and reads
// directly from row.Data[SlotIdx].
//
// The resolution follows the operator chain bottom-up, computing each
// operator's output schema and resolving its upstream expressions against
// the child's schema.
func ResolvePlanSlots(op DT.Operator) {
	_ = computeSchema(op) // compute bottom-up; for side effects via resolveExprs
}

// resolveExprs walks an operator's expression trees and resolves SlotIdx
// values using the given schema. If schema is nil or empty, it's a no-op.
func resolveExprs(op DT.Operator, schema []string) {
	if len(schema) == 0 {
		return
	}
	switch o := op.(type) {
	case *OP.Filter:
		if o.Predicate() != nil {
			resolveExprSlots(o.Predicate(), schema)
		}
	case *OP.Sort:
		for _, k := range o.Keys() {
			if k.Expr != nil {
				resolveExprSlots(k.Expr, schema)
			}
		}
	case *OP.Project:
		for _, c := range o.Cols() {
			resolveExprSlots(c, schema)
		}
	case *OP.NestedLoopJoin:
		// For join conditions, we don't resolve (too complex with dual schemas).
		// SlotIdx stays -1, falls back to runtime lookup.
	case *OP.HashJoin:
		// Same as NLJ — skip join condition resolution for now.
	case *OP.Limit:
		// No expression trees.
	case *OP.SeqScan:
		// Scan operators have no upstream expressions (no parent expressions here).
	case *OP.IndexScan:
	}
}

// computeSchema returns the output schema (column names) for an operator.
// For leaf operators (SeqScan, IndexScan) it reads from table metadata.
// For pass-through operators (Filter, Sort, Limit) it delegates to the child.
// For Project it extracts column names from the projection expressions.
// For Join operators it concatenates left and right schemas.
// Walks the operator tree recursively, resolving SlotIdx at each level.
func computeSchema(op DT.Operator) []string {
	if op == nil {
		return nil
	}

	switch o := op.(type) {
	case *OP.SeqScan:
		// Get schema from table metadata
		schema := tableSchema(o.Table())
		return schema

	case *OP.IndexScan:
		schema := tableSchema(o.Table())
		return schema

	case *OP.Filter:
		childSchema := computeSchema(childOf(o))
		resolveExprs(o, childSchema)
		return childSchema

	case *OP.Sort:
		childSchema := computeSchema(childOf(o))
		resolveExprs(o, childSchema)
		return childSchema

	case *OP.Limit:
		childSchema := computeSchema(childOf(o))
		return childSchema

	case *OP.Project:
		childSchema := computeSchema(childOf(o))
		resolveExprs(o, childSchema)
		// Project changes the schema — its output is defined by col expressions.
		return projectColNames(o.Cols())

	case *OP.NestedLoopJoin:
		leftSchema := computeSchema(o.LeftChild())
		rightSchema := computeSchema(o.RightChild())
		combined := make([]string, 0, len(leftSchema)+len(rightSchema))
		combined = append(combined, leftSchema...)
		combined = append(combined, rightSchema...)
		return combined

	case *OP.HashJoin:
		leftSchema := computeSchema(o.LeftChild())
		rightSchema := computeSchema(o.RightChild())
		combined := make([]string, 0, len(leftSchema)+len(rightSchema))
		combined = append(combined, leftSchema...)
		combined = append(combined, rightSchema...)
		return combined
	}

	return nil
}

func tableSchema(table string) []string {
	ss, ok := DT.SchemaFor(table)
	if ok && ss != nil {
		return ss.Cols
	}
	return nil
}

func childOf(op DT.Operator) DT.Operator {
	type childer interface{ Child() DT.Operator }
	if ch, ok := op.(childer); ok {
		return ch.Child()
	}
	return nil
}

func secondChild(op DT.Operator) DT.Operator {
	type righter interface{ Right() DT.Operator }
	if ch, ok := op.(righter); ok {
		return ch.Right()
	}
	return nil
}

// projectColNames extracts output column names from a list of projection
// expressions. For Ident and QualifiedName it uses the name directly;
// for other expression types it falls back to a placeholder.
func projectColNames(cols []PS.Expr) []string {
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		switch e := c.(type) {
		case *PS.Ident:
			names = append(names, e.Name)
		case *PS.QualifiedName:
			names = append(names, e.Table+"."+e.Name)
		case *PS.AliasedExpr:
			names = append(names, e.Alias)
		default:
			names = append(names, "")
		}
	}
	return names
}

// resolveExprSlots walks an expression tree and sets SlotIdx on each
// Ident and QualifiedName node by looking up the column name in schema.
func resolveExprSlots(expr PS.Expr, schema []string) {
	if expr == nil || len(schema) == 0 {
		return
	}
	switch e := expr.(type) {
	case *PS.Ident:
		for i, name := range schema {
			if strings.EqualFold(name, e.Name) {
				e.SlotIdx = i
				return
			}
		}
	case *PS.QualifiedName:
		// Only resolve when the FULL qualified name matches the schema.
		// Bare-name fallback is NOT used here because it would set SlotIdx
		// for correlated subquery references (e.g. QN{t1.b} in an inner
		// scan whose schema happens to contain "b" at a different index).
		// The runtime Lookup path handles prefix and bare-name matching.
		full := e.Table + "." + e.Name
		for i, name := range schema {
			if strings.EqualFold(name, full) {
				e.SlotIdx = i
				return
			}
		}
	case *PS.BinaryExpr:
		resolveExprSlots(e.Left, schema)
		resolveExprSlots(e.Right, schema)
	case *PS.UnaryExpr:
		resolveExprSlots(e.Operand, schema)
	case *PS.CastExpr:
		resolveExprSlots(e.Expr, schema)
	case *PS.CaseExpr:
		resolveExprSlots(e.Expr, schema)
		for _, w := range e.WhenList {
			resolveExprSlots(w.Cond, schema)
			resolveExprSlots(w.Then, schema)
		}
		resolveExprSlots(e.Else, schema)
	case *PS.AggregateFunc:
		resolveExprSlots(e.Arg, schema)
		resolveExprSlots(e.Separator, schema)
	case *PS.FunctionCall:
		for _, a := range e.Args {
			resolveExprSlots(a, schema)
		}
	case *PS.InExpr:
		resolveExprSlots(e.Expr, schema)
		for _, el := range e.List {
			resolveExprSlots(el, schema)
		}
	case *PS.BetweenExpr:
		resolveExprSlots(e.Expr, schema)
		resolveExprSlots(e.Low, schema)
		resolveExprSlots(e.High, schema)
	case *PS.SubqueryExpr:
		// Subqueries reference outer rows; skip resolution.
	case *PS.ExistsExpr:
		// Same — skip.
	}
}
