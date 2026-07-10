package OP

import (
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// joinRowsLL combines two rows into one output row. Callers that
// emit rows in a tight loop must pass `sharedColIndex` and
// `sharedCols` so the downstream Eval path can skip BuildColIndex
// per row (REQ000816). Passing nil for colIndex is allowed but
// forces a per-row colIndex build downstream.
func joinRowsLL(a, b *Row) Row {
	return joinRowsLLWithCols(a, b, nil, nil, nil)
}

// joinRowsLLWithCols is the full-form variant: caller supplies
// pre-built sharedCols, sharedTypes and sharedColIndex to avoid
// per-row allocation in the hot NLJ/HashCrossJoin path. When
// sharedCols is non-nil, out.Cols/Types share the slice (no copy).
// Data is always freshly allocated since it's per-row payload.
func joinRowsLLWithCols(a, b *Row, sharedCols []string, sharedTypes []LX.TokenType, sharedColIndex map[string]int) Row {
	out := Row{
		ColIndex: sharedColIndex,
	}
	if sharedCols != nil {
		out.Cols = sharedCols
	} else {
		nCols := len(a.Cols) + len(b.Cols)
		out.Cols = make([]string, 0, nCols)
		out.Cols = append(out.Cols, a.Cols...)
		out.Cols = append(out.Cols, b.Cols...)
	}
	if sharedTypes != nil {
		out.Types = sharedTypes
	} else {
		nTypes := len(a.Types) + len(b.Types)
		out.Types = make([]LX.TokenType, 0, nTypes)
		out.Types = append(out.Types, a.Types...)
		out.Types = append(out.Types, b.Types...)
	}
	nData := len(a.Data) + len(b.Data)
	out.Data = make([]Value, 0, nData)
	out.Data = append(out.Data, a.Data...)
	out.Data = append(out.Data, b.Data...)
	return out
}

// joinRowsProjected produces a join output row containing only the
// projected columns specified by layout. Each entry in layout is
// (leftIdx, rightIdx); exactly one is ≥0. REQ000803.
// Data is pre-allocated via the caller-supplied dataBuf slice; the
// function returns a Row with Data pointing into dataBuf.
func joinRowsProjected(a, b *Row, cols []string, types []LX.TokenType, colIndex map[string]int, layout [][2]int, dataBuf *[]Value) Row {
	off := len(*dataBuf)
	n := len(cols)
	// Ensure dataBuf has room for n Values.
	required := off + n
	if cap(*dataBuf) < required {
		newCap := cap(*dataBuf) * 2
		if newCap < required {
			newCap = required
		}
		buf := make([]Value, required, newCap)
		copy(buf, *dataBuf)
		*dataBuf = buf
	}
	*dataBuf = (*dataBuf)[:required]
	dataSlice := (*dataBuf)[off : off+n : off+n]
	for i, pair := range layout {
		if pair[0] >= 0 {
			dataSlice[i] = a.Data[pair[0]]
		} else if pair[1] >= 0 {
			dataSlice[i] = b.Data[pair[1]]
		}
		// pair[0] < 0 && pair[1] < 0: column not found, leave as zero Value (NULL)
	}
	return Row{
		Cols:     cols,
		Types:    types,
		Data:     dataSlice,
		ColIndex: colIndex,
	}
}

// buildProjectedLayout creates the projected column layout from a
// set of projected column names and the left/right column name lists.
// Returns (cols, types, colIndex, layout) for use with joinRowsProjected.
func buildProjectedLayout(projectedCols []string, leftCols, rightCols []string, leftTypes, rightTypes []int) ([]string, []int, map[string]int, [][2]int) {
	if projectedCols == nil {
		// No projection — use all columns.
		n := len(leftCols) + len(rightCols)
		allCols := make([]string, n)
		copy(allCols, leftCols)
		copy(allCols[len(leftCols):], rightCols)
		allTypes := make([]int, n)
		copy(allTypes, leftTypes)
		copy(allTypes[len(leftTypes):], rightTypes)
		layout := make([][2]int, n)
		for i := range leftCols {
			layout[i] = [2]int{i, -1}
		}
		for i := range rightCols {
			layout[len(leftCols)+i] = [2]int{-1, i}
		}
		colIndex := make(map[string]int, n)
		for i, c := range allCols {
			colIndex[strings.ToLower(c)] = i
		}
		return allCols, allTypes, colIndex, layout
	}

	n := len(projectedCols)
	cols := make([]string, n)
	types := make([]int, n)
	layout := make([][2]int, n)

	leftLower := make(map[string]int, len(leftCols))
	for i, c := range leftCols {
		leftLower[strings.ToLower(c)] = i
	}
	rightLower := make(map[string]int, len(rightCols))
	for i, c := range rightCols {
		rightLower[strings.ToLower(c)] = i
	}

	for i, pc := range projectedCols {
		cols[i] = pc
		pl := strings.ToLower(pc)
		if li, ok := leftLower[pl]; ok && li < len(leftTypes) {
			layout[i] = [2]int{li, -1}
			types[i] = leftTypes[li]
		} else if ri, ok := rightLower[pl]; ok && ri < len(rightTypes) {
			layout[i] = [2]int{-1, ri}
			types[i] = rightTypes[ri]
		} else {
			// Column not found — mark as invalid (will be skipped at output).
			layout[i] = [2]int{-2, -2}
		}
	}

	colIndex := make(map[string]int, n)
	for i, c := range cols {
		colIndex[strings.ToLower(c)] = i
	}
	return cols, types, colIndex, layout
}

// isJoinOp returns true when the operator is a NestedLoopJoin or
// HashJoin (i.e., it represents a join operator in the plan tree).
// REQ000843: used by tryHashCrossJoin to skip bushy-group joins.
// REQ001102: also includes MergeJoin.
func isJoinOp(op Operator) bool {
	switch op.(type) {
	case *NestedLoopJoin, *HashJoin, *HashCrossJoin, *MergeJoin:
		return true
	}
	return false
}

// schemaCols extracts column names from a schema.
func schemaCols(rows []Row) []string {
	if len(rows) == 0 {
		return nil
	}
	return append([]string(nil), rows[0].Cols...)
}

// schemaTypes extracts column types from a schema.
func schemaTypes(rows []Row) []LX.TokenType {
	if len(rows) == 0 {
		return nil
	}
	return append([]LX.TokenType(nil), rows[0].Types...)
}