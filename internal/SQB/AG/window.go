package AG

import (
	"context"
	"fmt"
	"slices"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// WindowOperator executes window functions over partitioned, sorted data.
type WindowOperator struct {
	input    Operator
	spec     *PS.WindowSpec
	funcName string
	args     []PS.Expr
	cols     []string
	rows     []Row
	results  []any
	idx      int
	outCols  []string
	outData  []any
}

// NewWindowOperator creates a window operator.
func NewWindowOperator(input Operator, funcName string, args []PS.Expr, spec *PS.WindowSpec, cols []string) *WindowOperator {
	return &WindowOperator{
		input:    input,
		spec:     spec,
		funcName: funcName,
		args:     args,
		cols:     cols,
		idx:      0,
	}
}

// Input returns the input operator feeding this window.
func (w *WindowOperator) Input() Operator { return w.input }

// FuncName returns the name of the window function (ROW_NUMBER, RANK, etc.).
func (w *WindowOperator) FuncName() string { return w.funcName }

// Args returns the window function arguments.
func (w *WindowOperator) Args() []PS.Expr { return w.args }

// Spec returns the window specification.
func (w *WindowOperator) Spec() *PS.WindowSpec { return w.spec }

// Cols returns the column names.
func (w *WindowOperator) Cols() []string { return w.cols }

func (w *WindowOperator) Next(ctx context.Context) (Row, error) {
	if w.rows == nil {
		if err := w.materialize(ctx); err != nil {
			return Row{}, err
		}
	}
	if w.idx >= len(w.rows) {
		return Row{}, ErrNoRows
	}
	row := w.rows[w.idx]
	result := w.results[w.idx]
	w.idx++

	if w.outCols == nil {
		if len(w.rows) > 0 && len(w.rows[0].Cols) > 0 {
			w.outCols = make([]string, len(w.rows[0].Cols)+1)
			copy(w.outCols, w.rows[0].Cols)
			w.outCols[len(w.rows[0].Cols)] = w.funcName
		} else {
			w.outCols = make([]string, len(w.cols)+1)
			copy(w.outCols, w.cols)
			w.outCols[len(w.cols)] = w.funcName
		}
	}
	outData := make([]Value, len(row.Data)+1)
	copy(outData, row.Data)
	outData[len(row.Data)] = DT.ValueFromAny(result)
	return Row{Cols: w.outCols, Data: outData}, nil
}

func (w *WindowOperator) Close() error {
	if w.input != nil {
		return w.input.Close()
	}
	return nil
}

func (w *WindowOperator) materialize(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		row, err := w.input.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return err
		}
		w.rows = append(w.rows, row)
	}

	if len(w.rows) == 0 {
		return nil
	}

	w.results = make([]any, len(w.rows))
	partitions := w.partitionRows()
	for _, part := range partitions {
		w.sortPartition(part)
		w.computeWindowFunc(part)
	}
	return nil
}

func (w *WindowOperator) partitionRows() [][]int {
	if len(w.spec.PartitionBy) == 0 {
		indices := make([]int, len(w.rows))
		for i := range indices {
			indices[i] = i
		}
		return [][]int{indices}
	}

	groups := make(map[string][]int)
	for i := range w.rows {
		key := w.partitionKey(&w.rows[i])
		groups[key] = append(groups[key], i)
	}

	result := make([][]int, 0, len(groups))
	for _, part := range groups {
		result = append(result, part)
	}
	return result
}

func (w *WindowOperator) partitionKey(row *Row) string {
	if len(w.spec.PartitionBy) == 0 {
		return ""
	}
	key := ""
	for i, expr := range w.spec.PartitionBy {
		val, err := EV.EvalValue(expr, row, nil)
		if err != nil {
			val = DT.NullValue()
		}
		if i > 0 {
			key += "|"
		}
		key += fmt.Sprintf("%v", val.ToAny())
	}
	return key
}

func (w *WindowOperator) sortPartition(indices []int) {
	if len(w.spec.OrderBy) == 0 {
		return
	}
	slices.SortStableFunc(indices, func(a, b int) int {
		for _, item := range w.spec.OrderBy {
			vi, _ := EV.EvalValue(item.Expr, &w.rows[a], nil)
			vj, _ := EV.EvalValue(item.Expr, &w.rows[b], nil)
			cmp := PL.CompareValue(vi, vj)
			if cmp != 0 {
				if item.Desc {
					return -cmp
				}
				return cmp
			}
		}
		return 0
	})
}

func (w *WindowOperator) computeWindowFunc(indices []int) {
	// Precompute peer-group boundaries for RANGE frames (REQ000530).
	// peerEnd[i] = last index (within indices) that has the same ORDER BY
	// value as the row at position i. Zero-valued when no ORDER BY.
	var peerEnd []int
	if w.spec.Frame != nil && w.spec.Frame.Type == "RANGE" && len(w.spec.OrderBy) > 0 {
		peerEnd = make([]int, len(indices))
		for i := range indices {
			peerEnd[i] = i
			for j := i + 1; j < len(indices); j++ {
				if w.sameOrderByGroup(indices[i], indices[j]) {
					peerEnd[i] = j
				} else {
					break
				}
			}
		}
	}

	switch w.funcName {
	case "ROW_NUMBER":
		for rank, idx := range indices {
			w.results[idx] = int64(rank + 1)
		}
	case "RANK":
		w.computeRank(indices, false)
	case "DENSE_RANK":
		w.computeRank(indices, true)
	case "PERCENT_RANK":
		w.computePercentRank(indices)
	case "CUME_DIST":
		w.computeCumeDist(indices)
	case "NTILE":
		w.computeNtile(indices)
	case "FIRST_VALUE":
		w.computeValueAtBound(indices, 0)
	case "LAST_VALUE":
		w.computeLastValue(indices)
	case "NTH_VALUE":
		w.computeNthValue(indices)
	case "LAG":
		w.computeLagLead(indices, -1)
	case "LEAD":
		w.computeLagLead(indices, 1)
	default:
		// Aggregate window functions: apply frame bounds (REQ000530).
		for pos, idx := range indices {
			lo, hi := 0, len(indices)-1
			if w.spec.Frame != nil {
				lo, hi = w.frameBounds(pos, indices, peerEnd)
			}
			if peerEnd != nil {
				hi = peerEnd[pos] // override: always include full peer group at upper bound
			}
			w.results[idx] = w.aggOverFrame(w.funcName, indices[lo:hi+1])
		}
	}
}

// sameOrderByGroup reports whether two rows have equal ORDER BY values.
func (w *WindowOperator) sameOrderByGroup(i, j int) bool {
	for _, item := range w.spec.OrderBy {
		vi, _ := EV.EvalValue(item.Expr, &w.rows[i], nil)
		vj, _ := EV.EvalValue(item.Expr, &w.rows[j], nil)
		if PL.CompareValue(vi, vj) != 0 {
			return false
		}
	}
	return true
}

// frameBounds returns the [lo, hi] index range (within the sorted indices
// slice) for the row at position pos, according to the ROWS/RANGE/GROUPS
// frame spec. REQ001353 adds GROUPS semantics: offsets count peer groups
// rather than physical rows; CURRENT_ROW covers the entire peer group.
func (w *WindowOperator) frameBounds(pos int, indices []int, peerEnd []int) (int, int) {
	frame := w.spec.Frame
	if frame == nil {
		return 0, len(indices) - 1
	}
	n := len(indices)
	lo := frameStart(frame.Start, pos, n)
	hi := frameEnd(frame.End, pos, n)
	switch frame.Type {
	case "RANGE":
		if frame.Start.Type == "CURRENT_ROW" {
			// Extend to first peer
			for lo > 0 && w.sameOrderByGroup(indices[lo-1], indices[pos]) {
				lo--
			}
		}
		if frame.End.Type == "CURRENT_ROW" && peerEnd != nil {
			hi = peerEnd[pos]
		}
	case "GROUPS":
		// Compute peer-group boundaries for pos: [peerStart, peerEndPos].
		peerStart := pos
		for i := pos - 1; i >= 0; i-- {
			if w.sameOrderByGroup(indices[i], indices[pos]) {
				peerStart = i
			} else {
				break
			}
		}
		peerEndPos := peerStart
		for i := peerStart + 1; i < n; i++ {
			if w.sameOrderByGroup(indices[i], indices[pos]) {
				peerEndPos = i
			} else {
				break
			}
		}
		// Resolve the lower bound: which group does the frame start in?
		switch frame.Start.Type {
		case "UNBOUNDED_PRECEDING":
			lo = 0
		case "CURRENT_ROW":
			lo = peerStart
		case "PRECEDING":
			off := evalBoundOffset(frame.Start.Offset)
			if off <= 0 {
				lo = peerStart
			} else {
				// Walk left off groups from peerStart. The frame lower bound
				// is the leftmost index of the group that is `off` groups
				// before pos. Stop at index 0 if we exhaust the partition.
				groupCount := 0
				target := peerStart
				for i := peerStart - 1; i >= 0; i-- {
					if !w.sameOrderByGroup(indices[i], indices[i+1]) {
						groupCount++
						if groupCount == off {
							// Walk further left to find the start of this group.
							for j := i - 1; j >= 0; j-- {
								if w.sameOrderByGroup(indices[j], indices[i]) {
									target = j
								} else {
									break
								}
							}
							if target > i {
								target = i
							}
							break
						}
					} else if i == 0 {
						target = 0
					}
				}
				if groupCount < off {
					target = 0
				}
				lo = target
			}
		case "FOLLOWING":
			off := evalBoundOffset(frame.Start.Offset)
			if off == 0 {
				lo = peerStart
			} else {
				groupCount := 0
				target := peerStart
				for i := peerEndPos + 1; i < n; i++ {
					if !w.sameOrderByGroup(indices[i], indices[i-1]) {
						groupCount++
						if groupCount == off {
							// Walk right to the end of this new group.
							end := i
							for j := i + 1; j < n; j++ {
								if w.sameOrderByGroup(indices[j], indices[i]) {
									end = j
								} else {
									break
								}
							}
							target = end
							break
						}
					} else if i == n-1 {
						target = n - 1
					}
				}
				if groupCount < off {
					target = n - 1
				}
				lo = target
			}
		}
		// Resolve the upper bound: which group does the frame end in?
		switch frame.End.Type {
		case "UNBOUNDED_FOLLOWING":
			hi = n - 1
		case "CURRENT_ROW":
			hi = peerEndPos
		case "PRECEDING":
			off := evalBoundOffset(frame.End.Offset)
			if off <= 0 {
				hi = peerEndPos
			} else {
				groupCount := 0
				target := peerEndPos
				for i := peerEndPos - 1; i >= 0; i-- {
					if !w.sameOrderByGroup(indices[i], indices[i+1]) {
						groupCount++
						if groupCount == off {
							for j := i - 1; j >= 0; j-- {
								if w.sameOrderByGroup(indices[j], indices[i]) {
									target = j
								} else {
									break
								}
							}
							if target > i {
								target = i
							}
							break
						}
					} else if i == 0 {
						target = 0
					}
				}
				if groupCount < off {
					target = 0
				}
				hi = target
			}
		case "FOLLOWING":
			off := evalBoundOffset(frame.End.Offset)
			if off == 0 {
				hi = peerEndPos
			} else {
				groupCount := 0
				target := peerEndPos
				for i := peerEndPos + 1; i < n; i++ {
					if !w.sameOrderByGroup(indices[i], indices[i-1]) {
						groupCount++
						if groupCount == off {
							end := i
							for j := i + 1; j < n; j++ {
								if w.sameOrderByGroup(indices[j], indices[i]) {
									end = j
								} else {
									break
								}
							}
							target = end
							break
						}
					} else if i == n-1 {
						target = n - 1
					}
				}
				if groupCount < off {
					target = n - 1
				}
				hi = target
			}
		}
	}
	return lo, hi
}

func frameStart(bound PS.FrameBound, pos, n int) int {
	switch bound.Type {
	case "UNBOUNDED_PRECEDING":
		return 0
	case "CURRENT_ROW":
		return pos
	case "PRECEDING":
		off := evalBoundOffset(bound.Offset)
		if off > pos {
			return 0
		}
		return pos - off
	case "FOLLOWING":
		off := evalBoundOffset(bound.Offset)
		end := pos + off
		if end >= n {
			return n - 1
		}
		return end
	default:
		return 0
	}
}

func frameEnd(bound PS.FrameBound, pos, n int) int {
	switch bound.Type {
	case "UNBOUNDED_FOLLOWING":
		return n - 1
	case "CURRENT_ROW":
		return pos
	case "PRECEDING":
		off := evalBoundOffset(bound.Offset)
		end := pos - off
		if end < 0 {
			return 0
		}
		return end
	case "FOLLOWING":
		off := evalBoundOffset(bound.Offset)
		end := pos + off
		if end >= n {
			return n - 1
		}
		return end
	default:
		return n - 1
	}
}

// evalBoundOffset evaluates a FrameBound offset expression to an int.
func evalBoundOffset(offset PS.Expr) int {
	if offset == nil {
		return 0
	}
	v, err := EV.EvalValue(offset, nil, nil)
	if err != nil {
		return 0
	}
	switch v.Kind {
	case KindInt:
		return int(v.I64)
	case KindFloat:
		return int(v.F64)
	default:
		return 0
	}
}

// aggOverFrame computes an aggregate over the given rows.
func (w *WindowOperator) aggOverFrame(funcName string, frameRows []int) any {
	if len(frameRows) == 0 {
		return nil
	}
	n := len(w.args)
	if n == 0 {
		return nil
	}
	var count int64
	var sum float64
	var min, max float64
	var hasVal bool
	for _, ri := range frameRows {
		val, err := EV.EvalValue(w.args[0], &w.rows[ri], nil)
		if err != nil || val.Kind == KindNull {
			continue
		}
		var fv float64
		switch val.Kind {
		case KindInt:
			fv = float64(val.I64)
		case KindFloat:
			fv = val.F64
		default:
			continue
		}
		count++
		sum += fv
		if !hasVal {
			min, max = fv, fv
			hasVal = true
		} else {
			if fv < min {
				min = fv
			}
			if fv > max {
				max = fv
			}
		}
	}
	switch funcName {
	case "SUM":
		if count == 0 {
			return nil
		}
		return sum
	case "AVG":
		if count == 0 {
			return nil
		}
		return sum / float64(count)
	case "MIN":
		if !hasVal {
			return nil
		}
		return min
	case "MAX":
		if !hasVal {
			return nil
		}
		return max
	case "COUNT":
		return count
	default:
		return nil
	}
}

func (w *WindowOperator) computeRank(indices []int, dense bool) {
	rank := int64(1)
	for i, idx := range indices {
		if i > 0 {
			prevIdx := indices[i-1]
			equal := true
			for _, item := range w.spec.OrderBy {
				vi, _ := EV.EvalValue(item.Expr, &w.rows[prevIdx], nil)
				vj, _ := EV.EvalValue(item.Expr, &w.rows[idx], nil)
				if PL.CompareValue(vi, vj) != 0 {
					equal = false
					break
				}
			}
			if !equal {
				if dense {
					rank++
				} else {
					rank = int64(i + 1)
				}
			}
		}
		w.results[idx] = rank
	}
}

func (w *WindowOperator) computeLagLead(indices []int, defaultOffset int) {
	n := len(w.args)
	defaultVal := any(nil)
	offset := defaultOffset

	// REQ000290: read offset from args[1] if provided
	if n >= 2 {
		if v, err := EV.EvalValue(w.args[1], nil, nil); err == nil {
			if ov, ok := DT.ToInt64(v); ok {
				offset = int(ov)
				if defaultOffset < 0 {
					offset = -offset // LAG: negative offset
				}
			}
		}
	}
	if n >= 3 {
		if v, err := EV.EvalValue(w.args[2], nil, nil); err == nil {
			defaultVal = v.ToAny()
		} else {
			defaultVal = nil
		}
	}

	for i, idx := range indices {
		srcPos := i + offset
		if srcPos < 0 || srcPos >= len(indices) {
			w.results[idx] = defaultVal
			continue
		}
		srcIdx := indices[srcPos]
		if n >= 1 {
			val, _ := EV.EvalValue(w.args[0], &w.rows[srcIdx], nil)
			w.results[idx] = val.ToAny()
		} else {
			w.results[idx] = defaultVal
		}
	}
}

// REQ001348: PERCENT_RANK = (rank - 1) / (partition_rows - 1).
// Single-row partitions return 0 to avoid divide-by-zero.
func (w *WindowOperator) computePercentRank(indices []int) {
	n := len(indices)
	if n == 0 {
		return
	}
	rank := int64(1)
	for i, idx := range indices {
		if i > 0 && !w.sameOrderByGroup(indices[i-1], indices[i]) {
			rank = int64(i + 1)
		}
		if n == 1 {
			w.results[idx] = float64(0)
		} else {
			w.results[idx] = float64(rank-1) / float64(n-1)
		}
	}
}

// REQ001349: CUME_DIST = (# rows with value <= current) / (partition size).
func (w *WindowOperator) computeCumeDist(indices []int) {
	n := len(indices)
	if n == 0 {
		return
	}
	for _, idx := range indices {
		// Count rows with value <= current, inclusive.
		count := 0
		for j := 0; j < n; j++ {
			vi, _ := EV.EvalValue(w.spec.OrderBy[0].Expr, &w.rows[indices[j]], nil)
			vj, _ := EV.EvalValue(w.spec.OrderBy[0].Expr, &w.rows[idx], nil)
			cmp := PL.CompareValue(vi, vj)
			if cmp <= 0 {
				count++
			}
		}
		w.results[idx] = float64(count) / float64(n)
	}
}

// REQ001350: NTILE(n) — distribute rows into n buckets as evenly as possible.
// Bucket index = ceil(rank * n / totalRows).
func (w *WindowOperator) computeNtile(indices []int) {
	n := len(indices)
	if n == 0 {
		return
	}
	buckets := 1
	if len(w.args) >= 1 {
		v, err := EV.EvalValue(w.args[0], nil, nil)
		if err == nil {
			if iv, ok := DT.ToInt64(v); ok && iv > 0 {
				buckets = int(iv)
			}
		}
	}
	for i, idx := range indices {
		rank := int64(i + 1)
		bucket := int((rank*int64(buckets) + int64(n) - 1) / int64(n))
		if bucket < 1 {
			bucket = 1
		}
		if bucket > buckets {
			bucket = buckets
		}
		w.results[idx] = int64(bucket)
	}
}

// REQ001351: FIRST_VALUE(expr) — value of expr at the first row of the frame.
func (w *WindowOperator) computeValueAtBound(indices []int, _ int) {
	n := len(indices)
	for pos, idx := range indices {
		lo, hi := 0, n-1
		if w.spec.Frame != nil {
			lo, hi = w.frameBounds(pos, indices, nil)
		}
		// Apply EXCLUDE on top of frame bounds.
		lo, hi = w.applyExclude(pos, lo, hi, indices)
		if hi < lo {
			w.results[idx] = nil
			continue
		}
		src := indices[lo]
		if len(w.args) >= 1 {
			val, _ := EV.EvalValue(w.args[0], &w.rows[src], nil)
			w.results[idx] = val.ToAny()
		}
	}
}

// REQ001351: LAST_VALUE(expr) — value of expr at the last row of the frame.
// Per SQL spec, default frame for LAST_VALUE without explicit frame is
// RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW; we honor whatever
// frame is supplied and fall back to the full partition.
func (w *WindowOperator) computeLastValue(indices []int) {
	n := len(indices)
	for pos, idx := range indices {
		lo, hi := 0, n-1
		if w.spec.Frame != nil {
			lo, hi = w.frameBounds(pos, indices, nil)
		}
		lo, hi = w.applyExclude(pos, lo, hi, indices)
		if hi < lo {
			w.results[idx] = nil
			continue
		}
		src := indices[hi]
		if len(w.args) >= 1 {
			val, _ := EV.EvalValue(w.args[0], &w.rows[src], nil)
			w.results[idx] = val.ToAny()
		}
	}
}

// REQ001352: NTH_VALUE(expr, n) — value of expr at the n-th row of the frame.
// 1-indexed; NULL when n is out of range.
func (w *WindowOperator) computeNthValue(indices []int) {
	n := len(indices)
	target := int64(0)
	if len(w.args) >= 2 {
		if v, err := EV.EvalValue(w.args[1], nil, nil); err == nil {
			if iv, ok := DT.ToInt64(v); ok {
				target = iv
			}
		}
	}
	for pos, idx := range indices {
		if target < 1 {
			w.results[idx] = nil
			continue
		}
		lo, hi := 0, n-1
		if w.spec.Frame != nil {
			lo, hi = w.frameBounds(pos, indices, nil)
		}
		lo, hi = w.applyExclude(pos, lo, hi, indices)
		frameSize := hi - lo + 1
		if hi < lo || int64(frameSize) < target {
			w.results[idx] = nil
			continue
		}
		src := indices[lo+int(target)-1]
		if len(w.args) >= 1 {
			val, _ := EV.EvalValue(w.args[0], &w.rows[src], nil)
			w.results[idx] = val.ToAny()
		}
	}
}

// applyExclude narrows [lo, hi] according to the EXCLUDE clause on the frame.
// REQ001354.
func (w *WindowOperator) applyExclude(pos, lo, hi int, indices []int) (int, int) {
	if w.spec.Frame == nil || w.spec.Frame.Exclude == "" || w.spec.Frame.Exclude == "NO_OTHERS" {
		return lo, hi
	}
	switch w.spec.Frame.Exclude {
	case "CURRENT_ROW":
		if pos >= lo && pos <= hi {
			if pos == hi {
				if hi > lo {
					return lo, hi - 1
				}
				return lo, lo - 1 // empty frame
			}
			if pos == lo {
				return lo + 1, hi
			}
			// pos in the middle — narrow [lo, hi] to the side that excludes pos.
			// Per spec, EXCLUDE CURRENT ROW removes only the current row; if
			// ordering matters, leave the surrounding frame in place and
			// mask at evaluation time. For the simple case the function
			// caller (FIRST/LAST/NTH) reads from lo or hi, so we shrink
			// toward whichever bound is not pos.
			if hi-pos > pos-lo {
				return lo, pos - 1
			}
			return pos + 1, hi
		}
	case "GROUP":
		// Find peer-group range containing pos.
		peerStart := pos
		for i := pos - 1; i >= lo; i-- {
			if w.sameOrderByGroup(indices[i], indices[pos]) {
				peerStart = i
			} else {
				break
			}
		}
		peerEnd := pos
		for i := pos + 1; i <= hi; i++ {
			if w.sameOrderByGroup(indices[i], indices[pos]) {
				peerEnd = i
			} else {
				break
			}
		}
		if peerEnd < hi {
			return lo, peerStart - 1
		}
		return lo, peerStart - 1
	case "TIES":
		// Drop peer rows of pos (keep only pos itself when present).
		peerStart := pos
		for i := pos - 1; i >= lo; i-- {
			if w.sameOrderByGroup(indices[i], indices[pos]) {
				peerStart = i
			} else {
				break
			}
		}
		peerEnd := pos
		for i := pos + 1; i <= hi; i++ {
			if w.sameOrderByGroup(indices[i], indices[pos]) {
				peerEnd = i
			} else {
				break
			}
		}
		if peerStart < pos {
			lo = pos + 1
		}
		if peerEnd > pos {
			hi = pos - 1
		}
		return lo, hi
	}
	return lo, hi
}
