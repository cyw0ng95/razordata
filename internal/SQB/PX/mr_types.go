package PX

import (
	"math"
	"strconv"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

type AccumKind int

const (
	AggCount AccumKind = iota
	AggSum
	AggMin
	AggMax
	AggAvg
	AggGroupConcat
	AggStringAgg
)

type AccumulatorSpec struct {
	Kind      AccumKind
	Col       int
	Separator string
	Distinct  bool
	Negate    bool
}

type UnifiedAccum struct {
	count    int64
	sumI     int64
	sumF     float64
	seenI    bool
	seenF    bool
	overflow bool

	hasValue bool
	best     AP.Value

	strParts []string
	strSep   string

	distinctSeen map[any]struct{}
}

func (a *UnifiedAccum) Update(spec *AccumulatorSpec, batch *UT.Batch, rowIdx int) {
	if spec.Kind == AggCount && spec.Col < 0 {
		a.count++
		return
	}

	if spec.Col < 0 || spec.Col >= len(batch.Cols) {
		return
	}
	col := batch.Cols[spec.Col]
	if col.Nulls != nil {
		if rowIdx < 0 || rowIdx >= len(col.Nulls) {
			return
		}
		if col.Nulls[rowIdx] {
			return
		}
	}

	v := UT.ToValue(col, rowIdx)
	if v.Kind == AP.KindNull {
		return
	}

	if spec.Distinct {
		key := v.ToAny()
		if a.distinctSeen == nil {
			a.distinctSeen = make(map[any]struct{})
		}
		if _, ok := a.distinctSeen[key]; ok {
			return
		}
		a.distinctSeen[key] = struct{}{}
	}

	switch spec.Kind {
	case AggCount:
		a.count++

	case AggSum, AggAvg:
		a.count++
		switch v.Kind {
		case AP.KindInt:
			val := v.I64
			if spec.Negate {
				val = -val
			}
			newSum, ok := addInt64Checked(a.sumI, val)
			if !ok {
				a.overflow = true
			} else {
				a.sumI = newSum
				a.seenI = true
			}
		case AP.KindFloat:
			val := v.F64
			if spec.Negate {
				val = -val
			}
			a.sumF += val
			a.seenF = true
		}

	case AggMin:
		if !a.hasValue || PL.CompareValue(PL.Value(v), PL.Value(a.best)) < 0 {
			a.best = v
			a.hasValue = true
		}

	case AggMax:
		if !a.hasValue || PL.CompareValue(PL.Value(v), PL.Value(a.best)) > 0 {
			a.best = v
			a.hasValue = true
		}

	case AggGroupConcat, AggStringAgg:
		switch v.Kind {
		case AP.KindText:
			a.strParts = append(a.strParts, v.S)
		case AP.KindInt:
			a.strParts = append(a.strParts, int64ToString(v.I64))
		case AP.KindFloat:
			a.strParts = append(a.strParts, float64ToString(v.F64))
		}
		a.strSep = spec.Separator
	}
}

func (a *UnifiedAccum) Merge(spec *AccumulatorSpec, other *UnifiedAccum) {
	if other == nil || other.count == 0 && !other.hasValue && len(other.strParts) == 0 {
		return
	}

	if spec.Distinct && other.distinctSeen != nil {
		a.mergeDistinct(spec, other)
		return
	}

	switch spec.Kind {
	case AggCount:
		a.count += other.count

	case AggSum, AggAvg:
		a.count += other.count
		if other.seenI {
			newSum, ok := addInt64Checked(a.sumI, other.sumI)
			if !ok {
				a.overflow = true
			} else {
				a.sumI = newSum
				a.seenI = a.seenI || other.seenI
			}
		}
		if other.seenF {
			a.sumF += other.sumF
			a.seenF = a.seenF || other.seenF
		}
		if other.overflow {
			a.overflow = true
		}

	case AggMin:
		if other.hasValue {
			if !a.hasValue || PL.CompareValue(PL.Value(other.best), PL.Value(a.best)) < 0 {
				a.best = other.best
				a.hasValue = true
			}
		}

	case AggMax:
		if other.hasValue {
			if !a.hasValue || PL.CompareValue(PL.Value(other.best), PL.Value(a.best)) > 0 {
				a.best = other.best
				a.hasValue = true
			}
		}

	case AggGroupConcat, AggStringAgg:
		a.strParts = append(a.strParts, other.strParts...)
		if a.strSep == "" {
			a.strSep = other.strSep
		}
	}
}

func (a *UnifiedAccum) mergeDistinct(spec *AccumulatorSpec, other *UnifiedAccum) {
	if a.distinctSeen == nil {
		a.distinctSeen = make(map[any]struct{}, len(other.distinctSeen))
	}

	for val := range other.distinctSeen {
		if _, exists := a.distinctSeen[val]; exists {
			continue
		}
		a.distinctSeen[val] = struct{}{}

		switch spec.Kind {
		case AggCount:
			a.count++
		case AggSum, AggAvg:
			a.count++
			switch v := val.(type) {
			case int64:
				newSum, ok := addInt64Checked(a.sumI, v)
				if !ok {
					a.overflow = true
				} else {
					a.sumI = newSum
					a.seenI = true
				}
			case float64:
				a.sumF += v
				a.seenF = true
			}
		case AggMin:
			v := anyToValue(val)
			if !a.hasValue || PL.CompareValue(v, PL.Value(a.best)) < 0 {
				a.best = AP.Value(v)
				a.hasValue = true
			}
		case AggMax:
			v := anyToValue(val)
			if !a.hasValue || PL.CompareValue(v, PL.Value(a.best)) > 0 {
				a.best = AP.Value(v)
				a.hasValue = true
			}
		case AggGroupConcat, AggStringAgg:
			switch v := val.(type) {
			case string:
				a.strParts = append(a.strParts, v)
			case int64:
				a.strParts = append(a.strParts, int64ToString(v))
			case float64:
				a.strParts = append(a.strParts, float64ToString(v))
			}
			a.strSep = spec.Separator
		}
	}
}

func anyToValue(v any) PL.Value {
	switch val := v.(type) {
	case int64:
		return PL.Value{Kind: AP.KindInt, I64: val}
	case float64:
		return PL.Value{Kind: AP.KindFloat, F64: val}
	case string:
		return PL.Value{Kind: AP.KindText, S: val}
	case bool:
		return PL.Value{Kind: AP.KindBool, Bo: val}
	default:
		return PL.Value{}
	}
}

func (a *UnifiedAccum) Result(spec *AccumulatorSpec) (any, bool) {
	switch spec.Kind {
	case AggCount:
		return a.count, true

	case AggSum:
		if a.overflow {
			return nil, false
		}
		if a.seenF {
			return a.sumF + float64(a.sumI), true
		}
		if a.seenI {
			return a.sumI, true
		}
		return nil, false

	case AggAvg:
		if a.count == 0 || a.overflow {
			return nil, false
		}
		if a.seenF {
			return (a.sumF + float64(a.sumI)) / float64(a.count), true
		}
		if a.seenI {
			return a.sumI / a.count, true
		}
		return nil, false

	case AggMin, AggMax:
		if !a.hasValue {
			return nil, false
		}
		return a.best.ToAny(), true

	case AggGroupConcat, AggStringAgg:
		if len(a.strParts) == 0 {
			return nil, false
		}
		sep := a.strSep
		if sep == "" {
			sep = ","
		}
		return strings.Join(a.strParts, sep), true
	}
	return nil, false
}

func (spec *AccumulatorSpec) ResultType(inputType LX.TokenType) LX.TokenType {
	switch spec.Kind {
	case AggCount:
		return LX.T_INT_KW
	case AggSum:
		return inputType
	case AggAvg:
		if inputType == LX.T_FLOAT_KW {
			return LX.T_FLOAT_KW
		}
		return LX.T_INT_KW
	case AggMin, AggMax:
		return inputType
	case AggGroupConcat, AggStringAgg:
		return LX.T_TEXT
	}
	return LX.T_INT_KW
}

func (a *UnifiedAccum) Reset() {
	a.count = 0
	a.sumI = 0
	a.sumF = 0
	a.seenI = false
	a.seenF = false
	a.overflow = false
	a.hasValue = false
	a.best = AP.Value{}
	a.strParts = a.strParts[:0]
	a.strSep = ""
	for k := range a.distinctSeen {
		delete(a.distinctSeen, k)
	}
}

func addInt64Checked(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

func int64ToString(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func (a *UnifiedAccum) debugHasValue() bool { return a.hasValue }

func float64ToString(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}