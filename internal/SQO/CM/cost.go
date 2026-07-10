package CM

import (
	AD "github.com/cyw0ng95/razordata/internal/SQB/AD"
	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	"github.com/cyw0ng95/razordata/internal/SQO/CP"
	"github.com/cyw0ng95/razordata/internal/SQO/SL"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

func Estimate(op pl.Operator, cp CP.CostParams) float64 {
	if cp.AbsoluteTime {
		return estimateAbsolute(op, cp)
	}
	return estimateLegacy(op)
}

func estimateLegacy(op pl.Operator) float64 {
	if op == nil {
		return 0
	}
	if aop, ok := op.(*AD.AdaptiveOp); ok {
		return estimateLegacy(aop.Inner)
	}
	switch v := op.(type) {
	case *OP.SeqScan:
		return 1.0
	case *OP.IndexScan:
		if v.IndexMode() {
			return 0.05
		}
		return 0.1
	case *OP.BitmapHeapScan:
		return 0.05*float64(len(v.IndexScans())) + 0.05
	case *OP.IndexOnlyScan:
		return 0.03
	case *OP.Filter:
		return estimateLegacy(v.Child()) * SL.EstimateSelectivity(v.Predicate())
	case *OP.Project:
		return estimateLegacy(v.Child())
	case *OP.Limit:
		return estimateLegacy(v.Child())
	case *OP.Offset:
		return estimateLegacy(v.Child())
	case *OP.Distinct:
		return estimateLegacy(v.Child())
	case *OP.Sort:
		cc := estimateLegacy(v.Child())
		if cc < 1 {
			cc = 1
		}
		return cc * (1 + log2ish(cc))
	case *AG.Aggregate:
		return estimateLegacy(v.Child()) + 1
	case *AG.HashAggregate:
		return estimateLegacy(v.Child()) + 1
	case *OP.NestedLoopJoin:
		lc := estimateLegacy(v.LeftChild())
		rc := estimateLegacy(v.RightChild())
		return lc * rc
	case *OP.HashJoin:
		lc := estimateLegacy(v.LeftChild())
		rc := estimateLegacy(v.RightChild())
		if lc < 1 {
			lc = 1
		}
		if rc < 1 {
			rc = 1
		}
		return lc + rc
	case *OP.HashCrossJoin:
		lc := estimateLegacy(v.LeftChild())
		rc := estimateLegacy(v.RightChild())
		if lc < 1 {
			lc = 1
		}
		if rc < 1 {
			rc = 1
		}
		return lc + rc
	case *OP.MergeJoin:
		lc := estimateLegacy(v.LeftChild())
		rc := estimateLegacy(v.RightChild())
		if lc < 1 {
			lc = 1
		}
		if rc < 1 {
			rc = 1
		}
		return lc + rc + 1
	case *WT.Insert, *WT.Update, *WT.Delete, *WT.CreateTable, *WT.DropTable:
		return 1.0
	default:
		return 1.0
	}
}

func estimatePGStyle(op pl.Operator, cp CP.CostParams) float64 {
	if op == nil {
		return 0
	}
	if aop, ok := op.(*AD.AdaptiveOp); ok {
		return estimatePGStyle(aop.Inner, cp)
	}
	switch v := op.(type) {
	case *OP.SeqScan:
		return cp.SeqPageCost
	case *OP.IndexScan:
		if v.IndexMode() {
			return cp.CPUIndexTupleCost + cp.RandomPageCost/100
		}
		return 2 * (cp.CPUIndexTupleCost + cp.RandomPageCost/100)
	case *OP.BitmapHeapScan:
		return 0.05*float64(len(v.IndexScans())) + 0.05
	case *OP.IndexOnlyScan:
		return cp.CPUIndexTupleCost
	case *OP.Filter:
		cc := estimatePGStyle(v.Child(), cp)
		sel := SL.EstimateSelectivity(v.Predicate())
		return cc + cc*sel*cp.CPUOperatorCost
	case *OP.Project:
		cc := estimatePGStyle(v.Child(), cp)
		return cc + cp.CPUTupleCost
	case *OP.Limit:
		return estimatePGStyle(v.Child(), cp)
	case *OP.Offset:
		return estimatePGStyle(v.Child(), cp)
	case *OP.Distinct:
		return estimatePGStyle(v.Child(), cp)
	case *OP.Sort:
		cc := estimatePGStyle(v.Child(), cp)
		if cc < 1 {
			cc = 1
		}
		return cc*(1+log2ish(cc)) + cc*cp.CPUOperatorCost
	case *AG.Aggregate:
		return estimatePGStyle(v.Child(), cp) + 1
	case *AG.HashAggregate:
		return estimatePGStyle(v.Child(), cp) + 1
	case *OP.NestedLoopJoin:
		lc := estimatePGStyle(v.LeftChild(), cp)
		rc := estimatePGStyle(v.RightChild(), cp)
		return lc * (rc + cp.CPUOperatorCost)
	case *OP.HashJoin:
		lc := estimatePGStyle(v.LeftChild(), cp)
		rc := estimatePGStyle(v.RightChild(), cp)
		if lc < 1 {
			lc = 1
		}
		if rc < 1 {
			rc = 1
		}
		return rc + lc*cp.CPUOperatorCost + rc*cp.CPUTupleCost
	case *OP.HashCrossJoin:
		lc := estimatePGStyle(v.LeftChild(), cp)
		rc := estimatePGStyle(v.RightChild(), cp)
		if lc < 1 {
			lc = 1
		}
		if rc < 1 {
			rc = 1
		}
		return lc + rc
	case *OP.MergeJoin:
		lc := estimatePGStyle(v.LeftChild(), cp)
		rc := estimatePGStyle(v.RightChild(), cp)
		if lc < 1 {
			lc = 1
		}
		if rc < 1 {
			rc = 1
		}
		return lc + rc + cp.CPUTupleCost
	case *WT.Insert, *WT.Update, *WT.Delete, *WT.CreateTable, *WT.DropTable:
		return 1.0
	default:
		return 1.0
	}
}

func estimateAbsolute(op pl.Operator, cp CP.CostParams) float64 {
	return estimatePGStyle(op, cp)
}

func log2ish(x float64) float64 {
	if x <= 1 {
		return 0
	}
	n := 0.0
	for x > 1 {
		x /= 2
		n++
	}
	return n
}