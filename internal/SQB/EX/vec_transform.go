package EX

import (
	"github.com/cyw0ng95/razordata/internal/SQB/AG"
	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// tryVectorizePlan attempts to replace row-based operators with vectorized
// equivalents. Returns the (possibly modified) root operator wrapped in
// a BatchToRowAdapter. Returns the original root unchanged if
// vectorization is not applicable.
func tryVectorizePlan(root DT.Operator) DT.Operator {
	if !isEligible(root) {
		return root
	}
	bp := transformOp(root)
	if bp == nil {
		return root
	}
	return UT.NewBatchToRowAdapter(bp)
}

// isEligible checks whether the operator tree can be vectorized.
func isEligible(root DT.Operator) bool {
	var check func(op DT.Operator) bool
	check = func(op DT.Operator) bool {
		if op == nil {
			return true
		}
		switch o := op.(type) {
		case *OP.SeqScan:
			return true
		case *OP.Filter:
			// VectorizedFilter wraps a BatchProducer child, but the
			// current MVP only supports a SeqScan as the immediate child.
			_, isSeq := o.Child().(*OP.SeqScan)
			return isSeq
		case *OP.Project:
			child := o.Child()
			_, isSeq := child.(*OP.SeqScan)
			if isSeq {
				return true
			}
			f, isFilt := child.(*OP.Filter)
			if isFilt {
				_, filtChildIsSeq := f.Child().(*OP.SeqScan)
				return filtChildIsSeq
			}
			return false
		case *AG.Aggregate:
			if len(o.GroupCols()) > 1 {
				return false
			}
			if len(o.GroupCols()) == 1 {
				if _, ok := o.GroupCols()[0].(*PS.Ident); !ok {
					return false
				}
			}
			child := o.Child()
			_, isSeq := child.(*OP.SeqScan)
			if isSeq {
				return true
			}
			f, isFilt := child.(*OP.Filter)
			if isFilt {
				_, filtChildIsSeq := f.Child().(*OP.SeqScan)
				return filtChildIsSeq
			}
			return false
		case *OP.HashJoin, *OP.NestedLoopJoin, *OP.Distinct, *OP.CompoundOp:
			return false
		default:
			return false
		}
	}
	return check(root)
}

// transformOp transforms a row operator tree into a BatchProducer chain.
// Returns nil if the operator cannot be vectorized.
func transformOp(op DT.Operator) UT.BatchProducer {
	if op == nil {
		return nil
	}
	switch o := op.(type) {
	case *OP.SeqScan:
		schema := extractSchema(o)
		types := extractTypes(o)
		return OP.NewVectorizedSeqScan(o, schema, types)
	case *OP.Filter:
		child := transformOp(o.Child())
		if child == nil {
			return nil
		}
		return OP.NewVectorizedFilter(child, o.Predicate())
	case *OP.Project:
		child := transformOp(o.Child())
		if child == nil {
			return nil
		}
		exprs := o.Cols()
		names := make([]string, len(exprs))
		for i, e := range exprs {
			names[i] = exprName(e)
		}
		return OP.NewVectorizedProject(child, exprs, names)
	case *AG.Aggregate:
		child := transformOp(o.Child())
		if child == nil {
			return nil
		}
		return transformAggregate(o, child)
	default:
		return nil
	}
}

// extractSchema gets column names from a SeqScan's store schema.
func extractSchema(s *OP.SeqScan) []string {
	sch := s.Schema()
	if sch == nil {
		return nil
	}
	names := make([]string, len(sch.Cols))
	for i, col := range sch.Cols {
		names[i] = col
	}
	return names
}

// extractTypes gets column types from a SeqScan's store schema.
func extractTypes(s *OP.SeqScan) []LX.TokenType {
	sch := s.Schema()
	if sch == nil {
		return nil
	}
	types := make([]LX.TokenType, len(sch.ColTypes))
	copy(types, sch.ColTypes)
	return types
}

// exprName extracts a human-readable name from an expression.
func exprName(e PS.Expr) string {
	switch expr := e.(type) {
	case *PS.Ident:
		return expr.Name
	case *PS.AliasedExpr:
		return expr.Alias
	case *PS.StarExpr:
		return "*"
	default:
		return "expr"
	}
}

// transformAggregate converts a row Aggregate to VectorizedHashAggregate.
func transformAggregate(a *AG.Aggregate, bp UT.BatchProducer) *AG.VectorizedHashAggregate {
	groupCols := a.GroupCols()
	// Multi-column GROUP BY: each column must be a simple Ident.
	if len(groupCols) > 0 {
	for _, gc := range groupCols {
		if _, ok := gc.(*PS.Ident); !ok {
			return nil // fallback to row-based for non-ident group cols
		}
	}
	}
	aggs := a.Aggs()
	if len(aggs) == 0 {
		return nil
	}
	defs := []AG.AggDef{{Kind: AG.AggCount}}
	groupColIdxs := make([]int, len(groupCols))
	for i := range groupCols {
		groupColIdxs[i] = i
	}
	if len(groupCols) == 0 {
		groupColIdxs = nil
	}
	return AG.NewVectorizedHashAggregate(bp, groupColIdxs, defs)
}
