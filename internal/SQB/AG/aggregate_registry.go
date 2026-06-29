// Package EX aggregate registry.
//
// REQ000978: replaces the flat `switch name` in evalAggregateOver
// with a map-based registry. Adding a new aggregate is a one-line
// registration in init() rather than editing a switch block.
package AG

import (
	"fmt"
	"strings"

	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// aggregateFuncImpl is the signature for a registry-resident
// aggregate function implementation.
type aggregateFuncImpl func(agg *PS.AggregateFunc, rows []Row, params []any) (any, error)

// aggregateFuncRegistry maps an aggregate name to its
// implementation. Populated at init time and read-only thereafter.
var AggregateFuncRegistry = map[string]aggregateFuncImpl{}

// registerAggregateFunc adds an entry to the registry. Called only
// from init; panics on duplicate registration so the build fails
// loudly if an aggregate is added twice.
func registerAggregateFunc(name string, impl aggregateFuncImpl) {
	if _, exists := AggregateFuncRegistry[name]; exists {
		panic("EX: duplicate aggregate function registration: " + name)
	}
	AggregateFuncRegistry[name] = impl
}

func init() {
	registerAggregateFunc("COUNT", evalAggCount)
	registerAggregateFunc("SUM", evalAggSum)
	registerAggregateFunc("AVG", evalAggAvg)
	registerAggregateFunc("MIN", evalAggMin)
	registerAggregateFunc("MAX", evalAggMax)
	registerAggregateFunc("GROUP_CONCAT", evalAggGroupConcat)
}

func evalAggCount(agg *PS.AggregateFunc, rows []Row, params []any) (any, error) {
	if agg.Distinct {
		seen := make(map[any]bool)
		for _, r := range rows {
			v, err := EV.EvalValue(agg.Arg, &r, params)
			if err != nil {
				return nil, err
			}
			if v.Kind == KindNull {
				continue
			}
			seen[v.ToAny()] = true
		}
		return int64(len(seen)), nil
	}
	if _, ok := agg.Arg.(*PS.StarExpr); ok {
		return int64(len(rows)), nil
	}
	var count int64
	for _, r := range rows {
		v, _ := EV.EvalValue(agg.Arg, &r, params)
		if v.Kind != KindNull {
			count++
		}
	}
	return count, nil
}

func evalAggSum(agg *PS.AggregateFunc, rows []Row, params []any) (any, error) {
	if agg.Distinct {
		return sumDistinct(agg, rows, params)
	}
	var sumI int64
	var sumF float64
	var seenI, seenF bool
	for _, r := range rows {
		v, err := EV.EvalValue(agg.Arg, &r, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue
		}
		if v.Kind == KindFloat {
			sumF += v.F64
			seenF = true
			continue
		}
		if v.Kind == KindInt {
			sumI += v.I64
			seenI = true
		}
	}
	if seenF {
		return sumF + float64(sumI), nil
	}
	if seenI {
		return sumI, nil
	}
	return nil, nil
}

func evalAggAvg(agg *PS.AggregateFunc, rows []Row, params []any) (any, error) {
	if agg.Distinct {
		return avgDistinct(agg, rows, params)
	}
	var sumF float64
	var n int64
	for _, r := range rows {
		v, err := EV.EvalValue(agg.Arg, &r, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue
		}
		if v.Kind == KindFloat {
			sumF += v.F64
			n++
		} else if v.Kind == KindInt {
			sumF += float64(v.I64)
			n++
		}
	}
	if n == 0 {
		return nil, nil
	}
	return sumF / float64(n), nil
}

func evalAggMin(agg *PS.AggregateFunc, rows []Row, params []any) (any, error) {
	if agg.Distinct {
		return minDistinct(agg, rows, params)
	}
	var best Value
	for _, r := range rows {
		v, err := EV.EvalValue(agg.Arg, &r, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue
		}
		if best.Kind == KindNull || PL.CompareValue(v, best) < 0 {
			best = v
		}
	}
	return best.ToAny(), nil
}

func evalAggMax(agg *PS.AggregateFunc, rows []Row, params []any) (any, error) {
	if agg.Distinct {
		return maxDistinct(agg, rows, params)
	}
	var best Value
	for _, r := range rows {
		v, err := EV.EvalValue(agg.Arg, &r, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue
		}
		if best.Kind == KindNull || PL.CompareValue(v, best) > 0 {
			best = v
		}
	}
	return best.ToAny(), nil
}

func evalAggGroupConcat(agg *PS.AggregateFunc, rows []Row, params []any) (any, error) {
	sep := ","
	if agg.Separator != nil {
		sv, err := EV.EvalValue(agg.Separator, nil, params)
		if err != nil {
			return nil, err
		}
		if sv.Kind != KindNull {
			sep = fmt.Sprintf("%v", sv.ToAny())
		}
	}
	var parts []string
	seen := make(map[any]bool)
	for _, r := range rows {
		v, err := EV.EvalValue(agg.Arg, &r, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue
		}
		if agg.Distinct {
			if seen[v.ToAny()] {
				continue
			}
			seen[v.ToAny()] = true
		}
		parts = append(parts, fmt.Sprintf("%v", v.ToAny()))
	}
	if len(parts) == 0 {
		return nil, nil
	}
	var b strings.Builder
	total := len(parts[0])
	for _, p := range parts[1:] {
		total += len(sep) + len(p)
	}
	b.Grow(total)
	b.WriteString(parts[0])
	for _, p := range parts[1:] {
		b.WriteString(sep)
		b.WriteString(p)
	}
	return b.String(), nil
}
