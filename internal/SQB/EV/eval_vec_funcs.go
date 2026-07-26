package EV

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func evalFunctionBatchExpr(e *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	switch strings.ToUpper(e.Name) {
	case "ABS":
		return evalAbsBatch(e, batch, params)
	case "LENGTH":
		return evalLengthBatch(e, batch, params)
	case "MOD":
		return evalModBatch(e, batch, params)
	case "UPPER":
		return evalUpperBatch(e, batch, params)
	case "LOWER":
		return evalLowerBatch(e, batch, params)
	case "SIGN":
		return evalSignBatch(e, batch, params)
	case "OCTET_LENGTH":
		return evalOctetLengthBatch(e, batch, params)
	case "SQLITE_VERSION":
		return FillLiteralColumn(batch, LX.T_TEXT, "0.26.7")
	case "SQLITE_SOURCE_ID":
		return FillLiteralColumn(batch, LX.T_TEXT, "razordata-v0.26.7")
	case "COALESCE", "IFNULL":
		return evalCoalesceBatch(e, batch, params)
	case "NULLIF":
		return evalNullIfBatch(e, batch, params)
	case "SUBSTR", "SUBSTRING":
		return evalSubstrBatch(e, batch, params)
	case "REPLACE":
		return evalReplaceBatch(e, batch, params)
	case "TRIM":
		return evalTrimBatch(e, batch, params)
	case "ROUND":
		return evalRoundBatch(e, batch, params)
	case "TYPEOF":
		return evalTypeofBatch(e, batch, params)
	case "INSTR":
		return evalInstrBatch(e, batch, params)
	case "HEX":
		return evalHexBatch(e, batch, params)
	default:
		return evalRowFallbackColumn(e, batch, params)
	}
}

func evalAbsBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) != 1 {
		return evalRowFallbackColumn(call, batch, params)
	}
	arg := EvalBatchExpr(call.Args[0], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_INT_KW}
	}

	switch arg.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		out := UT.Column{Type: LX.T_INT_KW, Data: UT.ColumnData{Ints: make([]int64, batch.Size)}}
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				i := int(idx)
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				v := arg.Data.Ints[i]
				if v == math.MinInt64 {
					setNull(&out, i)
					continue
				}
				if v < 0 {
					out.Data.Ints[i] = -v
				} else {
					out.Data.Ints[i] = v
				}
			}
		} else {
			for i := 0; i < n; i++ {
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				v := arg.Data.Ints[i]
				if v == math.MinInt64 {
					setNull(&out, i)
					continue
				}
				if v < 0 {
					out.Data.Ints[i] = -v
				} else {
					out.Data.Ints[i] = v
				}
			}
		}
		return out
	case LX.T_FLOAT_KW:
		out := UT.Column{Type: LX.T_FLOAT_KW, Data: UT.ColumnData{Floats: make([]float64, batch.Size)}}
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				i := int(idx)
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				v := arg.Data.Floats[i]
				if v < 0 {
					out.Data.Floats[i] = -v
				} else {
					out.Data.Floats[i] = v
				}
			}
		} else {
			for i := 0; i < n; i++ {
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				v := arg.Data.Floats[i]
				if v < 0 {
					out.Data.Floats[i] = -v
				} else {
					out.Data.Floats[i] = v
				}
			}
		}
		return out
	default:
		return evalRowFallbackColumn(call, batch, params)
	}
}

func evalLengthBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) != 1 {
		return evalRowFallbackColumn(call, batch, params)
	}
	arg := EvalBatchExpr(call.Args[0], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_INT_KW}
	}

	if arg.Type == LX.T_TEXT || arg.Type == LX.T_VARCHAR || arg.Type == LX.T_BLOB {
		out := UT.Column{Type: LX.T_INT_KW, Data: UT.ColumnData{Ints: make([]int64, batch.Size)}}
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				i := int(idx)
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				out.Data.Ints[i] = int64(len(arg.Data.Strs[i]))
			}
		} else {
			for i := 0; i < n; i++ {
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				out.Data.Ints[i] = int64(len(arg.Data.Strs[i]))
			}
		}
		return out
	}
	return evalRowFallbackColumn(call, batch, params)
}

func evalUpperBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) != 1 {
		return evalRowFallbackColumn(call, batch, params)
	}
	arg := EvalBatchExpr(call.Args[0], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_TEXT}
	}

	if isTextColumn(arg) {
		out := UT.Column{Type: LX.T_TEXT, Data: UT.ColumnData{Strs: make([]string, batch.Size)}}
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				i := int(idx)
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				out.Data.Strs[i] = strings.ToUpper(arg.Data.Strs[i])
			}
		} else {
			for i := 0; i < n; i++ {
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				out.Data.Strs[i] = strings.ToUpper(arg.Data.Strs[i])
			}
		}
		return out
	}
	return evalRowFallbackColumn(call, batch, params)
}

func evalLowerBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) != 1 {
		return evalRowFallbackColumn(call, batch, params)
	}
	arg := EvalBatchExpr(call.Args[0], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_TEXT}
	}

	if isTextColumn(arg) {
		out := UT.Column{Type: LX.T_TEXT, Data: UT.ColumnData{Strs: make([]string, batch.Size)}}
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				i := int(idx)
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				out.Data.Strs[i] = strings.ToLower(arg.Data.Strs[i])
			}
		} else {
			for i := 0; i < n; i++ {
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				out.Data.Strs[i] = strings.ToLower(arg.Data.Strs[i])
			}
		}
		return out
	}
	return evalRowFallbackColumn(call, batch, params)
}

func evalModBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) != 2 {
		return evalRowFallbackColumn(call, batch, params)
	}
	left := EvalBatchExpr(call.Args[0], batch, params)
	right := EvalBatchExpr(call.Args[1], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_INT_KW}
	}

	lt := left.Type
	rt := right.Type
	if (lt == LX.T_INT_KW || lt == LX.T_BIGINT) && (rt == LX.T_INT_KW || rt == LX.T_BIGINT) {
		out := UT.Column{Type: LX.T_INT_KW, Data: UT.ColumnData{Ints: make([]int64, batch.Size)}}
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				i := int(idx)
				if isNull(left, i) || isNull(right, i) {
					setNull(&out, i)
					continue
				}
				if right.Data.Ints[i] == 0 {
					setNull(&out, i)
					continue
				}
				out.Data.Ints[i] = left.Data.Ints[i] % right.Data.Ints[i]
			}
		} else {
			for i := 0; i < n; i++ {
				if isNull(left, i) || isNull(right, i) {
					setNull(&out, i)
					continue
				}
				if right.Data.Ints[i] == 0 {
					setNull(&out, i)
					continue
				}
				out.Data.Ints[i] = left.Data.Ints[i] % right.Data.Ints[i]
			}
		}
		return out
	}
	return evalRowFallbackColumn(call, batch, params)
}

func evalSignBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) != 1 {
		return evalRowFallbackColumn(call, batch, params)
	}
	arg := EvalBatchExpr(call.Args[0], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_INT_KW}
	}

	switch arg.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		out := UT.Column{Type: LX.T_INT_KW, Data: UT.ColumnData{Ints: make([]int64, batch.Size)}}
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				i := int(idx)
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				v := arg.Data.Ints[i]
				switch {
				case v < 0:
					out.Data.Ints[i] = -1
				case v > 0:
					out.Data.Ints[i] = 1
				default:
					out.Data.Ints[i] = 0
				}
			}
		} else {
			for i := 0; i < n; i++ {
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				v := arg.Data.Ints[i]
				switch {
				case v < 0:
					out.Data.Ints[i] = -1
				case v > 0:
					out.Data.Ints[i] = 1
				default:
					out.Data.Ints[i] = 0
				}
			}
		}
		return out
	case LX.T_FLOAT_KW:
		out := UT.Column{Type: LX.T_INT_KW, Data: UT.ColumnData{Ints: make([]int64, batch.Size)}}
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				i := int(idx)
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				v := arg.Data.Floats[i]
				switch {
				case v < 0:
					out.Data.Ints[i] = -1
				case v > 0:
					out.Data.Ints[i] = 1
				default:
					out.Data.Ints[i] = 0
				}
			}
		} else {
			for i := 0; i < n; i++ {
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				v := arg.Data.Floats[i]
				switch {
				case v < 0:
					out.Data.Ints[i] = -1
				case v > 0:
					out.Data.Ints[i] = 1
				default:
					out.Data.Ints[i] = 0
				}
			}
		}
		return out
	default:
		return evalRowFallbackColumn(call, batch, params)
	}
}

func evalOctetLengthBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) != 1 {
		return evalRowFallbackColumn(call, batch, params)
	}
	arg := EvalBatchExpr(call.Args[0], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_INT_KW}
	}

	if arg.Type == LX.T_TEXT || arg.Type == LX.T_VARCHAR || arg.Type == LX.T_BLOB {
		out := UT.Column{Type: LX.T_INT_KW, Data: UT.ColumnData{Ints: make([]int64, batch.Size)}}
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				i := int(idx)
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				out.Data.Ints[i] = int64(len(arg.Data.Strs[i]))
			}
		} else {
			for i := 0; i < n; i++ {
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				out.Data.Ints[i] = int64(len(arg.Data.Strs[i]))
			}
		}
		return out
	}
	return evalRowFallbackColumn(call, batch, params)
}

// ── REQ002074: batch-native function coverage ──

func evalCoalesceBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) < 1 {
		return evalRowFallbackColumn(call, batch, params)
	}
	args := make([]UT.Column, len(call.Args))
	for i, arg := range call.Args {
		args[i] = EvalBatchExpr(arg, batch, params)
	}
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_NULL}
	}

	// Determine output type from first non-null arg column type.
	outType := LX.T_NULL
	for _, arg := range args {
		if arg.Type != LX.T_NULL {
			outType = arg.Type
			break
		}
	}

	out := UT.Column{Type: outType}
	allocateColumnData(&out, batch.Size)

	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			i := int(idx)
			found := false
			for _, arg := range args {
				if !isNull(arg, i) {
					copyColumnValue(&out, i, arg, i)
					found = true
					break
				}
			}
			if !found {
				setNull(&out, i)
			}
		}
	} else {
		for i := 0; i < n; i++ {
			found := false
			for _, arg := range args {
				if !isNull(arg, i) {
					copyColumnValue(&out, i, arg, i)
					found = true
					break
				}
			}
			if !found {
				setNull(&out, i)
			}
		}
	}
	return out
}

func evalNullIfBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) != 2 {
		return evalRowFallbackColumn(call, batch, params)
	}
	left := EvalBatchExpr(call.Args[0], batch, params)
	right := EvalBatchExpr(call.Args[1], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: left.Type}
	}

	out := UT.Column{Type: left.Type}
	allocateColumnData(&out, batch.Size)

	lt := left.Type
	rt := right.Type

	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			i := int(idx)
			if isNull(left, i) || isNull(right, i) {
				if isNull(left, i) {
					setNull(&out, i)
				} else {
					copyColumnValue(&out, i, left, i)
				}
				continue
			}
			equal := false
			if (lt == LX.T_INT_KW || lt == LX.T_BIGINT) && (rt == LX.T_INT_KW || rt == LX.T_BIGINT) {
				equal = left.Data.Ints[i] == right.Data.Ints[i]
			} else if lt == LX.T_FLOAT_KW && rt == LX.T_FLOAT_KW {
				equal = left.Data.Floats[i] == right.Data.Floats[i]
			} else if isTextColumn(left) && isTextColumn(right) {
				equal = left.Data.Strs[i] == right.Data.Strs[i]
			} else {
				return evalRowFallbackColumn(call, batch, params)
			}
			if equal {
				setNull(&out, i)
			} else {
				copyColumnValue(&out, i, left, i)
			}
		}
	} else {
		for i := 0; i < n; i++ {
			if isNull(left, i) || isNull(right, i) {
				if isNull(left, i) {
					setNull(&out, i)
				} else {
					copyColumnValue(&out, i, left, i)
				}
				continue
			}
			equal := false
			if (lt == LX.T_INT_KW || lt == LX.T_BIGINT) && (rt == LX.T_INT_KW || rt == LX.T_BIGINT) {
				equal = left.Data.Ints[i] == right.Data.Ints[i]
			} else if lt == LX.T_FLOAT_KW && rt == LX.T_FLOAT_KW {
				equal = left.Data.Floats[i] == right.Data.Floats[i]
			} else if isTextColumn(left) && isTextColumn(right) {
				equal = left.Data.Strs[i] == right.Data.Strs[i]
			} else {
				return evalRowFallbackColumn(call, batch, params)
			}
			if equal {
				setNull(&out, i)
			} else {
				copyColumnValue(&out, i, left, i)
			}
		}
	}
	return out
}

func evalSubstrBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) < 2 || len(call.Args) > 3 {
		return evalRowFallbackColumn(call, batch, params)
	}
	strCol := EvalBatchExpr(call.Args[0], batch, params)
	startCol := EvalBatchExpr(call.Args[1], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_TEXT}
	}

	if !isTextColumn(strCol) {
		return evalRowFallbackColumn(call, batch, params)
	}
	if startCol.Type != LX.T_INT_KW && startCol.Type != LX.T_BIGINT {
		return evalRowFallbackColumn(call, batch, params)
	}

	var lenCol UT.Column
	hasLen := len(call.Args) == 3
	if hasLen {
		lenCol = EvalBatchExpr(call.Args[2], batch, params)
		if lenCol.Type != LX.T_INT_KW && lenCol.Type != LX.T_BIGINT {
			return evalRowFallbackColumn(call, batch, params)
		}
	}

	out := UT.Column{Type: LX.T_TEXT, Data: UT.ColumnData{Strs: make([]string, batch.Size)}}

	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			i := int(idx)
			if isNull(strCol, i) || isNull(startCol, i) {
				setNull(&out, i)
				continue
			}
			if hasLen && isNull(lenCol, i) {
				setNull(&out, i)
				continue
			}
			out.Data.Strs[i] = substrRow(strCol.Data.Strs[i], startCol.Data.Ints[i], hasLen, lenCol, i)
		}
	} else {
		for i := 0; i < n; i++ {
			if isNull(strCol, i) || isNull(startCol, i) {
				setNull(&out, i)
				continue
			}
			if hasLen && isNull(lenCol, i) {
				setNull(&out, i)
				continue
			}
			out.Data.Strs[i] = substrRow(strCol.Data.Strs[i], startCol.Data.Ints[i], hasLen, lenCol, i)
		}
	}
	return out
}

// substrRow computes SUBSTR(s, start[, length]) for a single row.
func substrRow(s string, start int64, hasLen bool, lenCol UT.Column, i int) string {
	runeStr := []rune(s)
	strLen := int64(len(runeStr))

	var pos int64
	if start <= 0 {
		pos = 0
	} else {
		pos = start - 1
	}

	if pos >= strLen {
		return ""
	}

	if !hasLen {
		return string(runeStr[pos:])
	}
	length := lenCol.Data.Ints[i]
	if length <= 0 {
		return ""
	}
	end := pos + length
	if end > strLen {
		end = strLen
	}
	return string(runeStr[pos:end])
}

func evalReplaceBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) != 3 {
		return evalRowFallbackColumn(call, batch, params)
	}
	strCol := EvalBatchExpr(call.Args[0], batch, params)
	fromCol := EvalBatchExpr(call.Args[1], batch, params)
	toCol := EvalBatchExpr(call.Args[2], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_TEXT}
	}

	if !isTextColumn(strCol) || !isTextColumn(fromCol) || !isTextColumn(toCol) {
		return evalRowFallbackColumn(call, batch, params)
	}

	out := UT.Column{Type: LX.T_TEXT, Data: UT.ColumnData{Strs: make([]string, batch.Size)}}

	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			i := int(idx)
			if isNull(strCol, i) || isNull(fromCol, i) || isNull(toCol, i) {
				setNull(&out, i)
				continue
			}
			out.Data.Strs[i] = strings.ReplaceAll(strCol.Data.Strs[i], fromCol.Data.Strs[i], toCol.Data.Strs[i])
		}
	} else {
		for i := 0; i < n; i++ {
			if isNull(strCol, i) || isNull(fromCol, i) || isNull(toCol, i) {
				setNull(&out, i)
				continue
			}
			out.Data.Strs[i] = strings.ReplaceAll(strCol.Data.Strs[i], fromCol.Data.Strs[i], toCol.Data.Strs[i])
		}
	}
	return out
}

func evalTrimBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) != 1 {
		return evalRowFallbackColumn(call, batch, params)
	}
	arg := EvalBatchExpr(call.Args[0], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_TEXT}
	}

	if !isTextColumn(arg) {
		return evalRowFallbackColumn(call, batch, params)
	}

	out := UT.Column{Type: LX.T_TEXT, Data: UT.ColumnData{Strs: make([]string, batch.Size)}}

	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			i := int(idx)
			if isNull(arg, i) {
				setNull(&out, i)
				continue
			}
			out.Data.Strs[i] = strings.TrimSpace(arg.Data.Strs[i])
		}
	} else {
		for i := 0; i < n; i++ {
			if isNull(arg, i) {
				setNull(&out, i)
				continue
			}
			out.Data.Strs[i] = strings.TrimSpace(arg.Data.Strs[i])
		}
	}
	return out
}

func evalRoundBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) < 1 || len(call.Args) > 2 {
		return evalRowFallbackColumn(call, batch, params)
	}
	arg := EvalBatchExpr(call.Args[0], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_FLOAT_KW}
	}

	if arg.Type != LX.T_FLOAT_KW {
		return evalRowFallbackColumn(call, batch, params)
	}

	var precCol UT.Column
	hasPrec := len(call.Args) == 2
	if hasPrec {
		precCol = EvalBatchExpr(call.Args[1], batch, params)
		if precCol.Type != LX.T_INT_KW && precCol.Type != LX.T_BIGINT {
			return evalRowFallbackColumn(call, batch, params)
		}
	}

	out := UT.Column{Type: LX.T_FLOAT_KW, Data: UT.ColumnData{Floats: make([]float64, batch.Size)}}

	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			i := int(idx)
			if isNull(arg, i) {
				setNull(&out, i)
				continue
			}
			if hasPrec && isNull(precCol, i) {
				setNull(&out, i)
				continue
			}
			v := arg.Data.Floats[i]
			var prec int64
			if hasPrec {
				prec = precCol.Data.Ints[i]
			}
			out.Data.Floats[i] = roundToPrec(v, prec)
		}
	} else {
		for i := 0; i < n; i++ {
			if isNull(arg, i) {
				setNull(&out, i)
				continue
			}
			if hasPrec && isNull(precCol, i) {
				setNull(&out, i)
				continue
			}
			v := arg.Data.Floats[i]
			var prec int64
			if hasPrec {
				prec = precCol.Data.Ints[i]
			}
			out.Data.Floats[i] = roundToPrec(v, prec)
		}
	}
	return out
}

// roundToPrec rounds x to n decimal places using math.Round(x*pow10)/pow10.
func roundToPrec(x float64, n int64) float64 {
	if n <= 0 {
		return math.Round(x)
	}
	p := math.Pow10(int(n))
	return math.Round(x*p) / p
}

func evalTypeofBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) != 1 {
		return evalRowFallbackColumn(call, batch, params)
	}
	arg := EvalBatchExpr(call.Args[0], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_TEXT}
	}

	out := UT.Column{Type: LX.T_TEXT, Data: UT.ColumnData{Strs: make([]string, batch.Size)}}

	var typeStr string
	switch arg.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		typeStr = "integer"
	case LX.T_FLOAT_KW:
		typeStr = "real"
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		typeStr = "text"
	case LX.T_NULL:
		typeStr = "null"
	default:
		return evalRowFallbackColumn(call, batch, params)
	}

	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			i := int(idx)
			if isNull(arg, i) {
				out.Data.Strs[i] = "null"
			} else {
				out.Data.Strs[i] = typeStr
			}
		}
	} else {
		for i := 0; i < n; i++ {
			if isNull(arg, i) {
				out.Data.Strs[i] = "null"
			} else {
				out.Data.Strs[i] = typeStr
			}
		}
	}
	return out
}

func evalInstrBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) != 2 {
		return evalRowFallbackColumn(call, batch, params)
	}
	strCol := EvalBatchExpr(call.Args[0], batch, params)
	subCol := EvalBatchExpr(call.Args[1], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_INT_KW}
	}

	if !isTextColumn(strCol) || !isTextColumn(subCol) {
		return evalRowFallbackColumn(call, batch, params)
	}

	out := UT.Column{Type: LX.T_INT_KW, Data: UT.ColumnData{Ints: make([]int64, batch.Size)}}

	if batch.Sel != nil {
		for _, idx := range batch.Sel {
			i := int(idx)
			if isNull(strCol, i) || isNull(subCol, i) {
				setNull(&out, i)
				continue
			}
			out.Data.Ints[i] = int64(strings.Index(strCol.Data.Strs[i], subCol.Data.Strs[i]) + 1)
		}
	} else {
		for i := 0; i < n; i++ {
			if isNull(strCol, i) || isNull(subCol, i) {
				setNull(&out, i)
				continue
			}
			out.Data.Ints[i] = int64(strings.Index(strCol.Data.Strs[i], subCol.Data.Strs[i]) + 1)
		}
	}
	return out
}

func evalHexBatch(call *PS.FunctionCall, batch *UT.Batch, params []any) UT.Column {
	if len(call.Args) != 1 {
		return evalRowFallbackColumn(call, batch, params)
	}
	arg := EvalBatchExpr(call.Args[0], batch, params)
	n := batch.LogicalSize()
	if n == 0 {
		return UT.Column{Type: LX.T_TEXT}
	}

	out := UT.Column{Type: LX.T_TEXT, Data: UT.ColumnData{Strs: make([]string, batch.Size)}}

	switch arg.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				i := int(idx)
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				out.Data.Strs[i] = fmt.Sprintf("%X", arg.Data.Ints[i])
			}
		} else {
			for i := 0; i < n; i++ {
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				out.Data.Strs[i] = fmt.Sprintf("%X", arg.Data.Ints[i])
			}
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				i := int(idx)
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				out.Data.Strs[i] = hex.EncodeToString([]byte(arg.Data.Strs[i]))
			}
		} else {
			for i := 0; i < n; i++ {
				if isNull(arg, i) {
					setNull(&out, i)
					continue
				}
				out.Data.Strs[i] = hex.EncodeToString([]byte(arg.Data.Strs[i]))
			}
		}
	default:
		return evalRowFallbackColumn(call, batch, params)
	}
	return out
}

// copyColumnValue copies the value at row srcIdx of src into row dstIdx of dst.
func copyColumnValue(dst *UT.Column, dstIdx int, src UT.Column, srcIdx int) {
	switch dst.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if src.Type == LX.T_INT_KW || src.Type == LX.T_BIGINT {
			dst.Data.Ints[dstIdx] = src.Data.Ints[srcIdx]
		}
	case LX.T_FLOAT_KW:
		if src.Type == LX.T_FLOAT_KW {
			dst.Data.Floats[dstIdx] = src.Data.Floats[srcIdx]
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if isTextColumn(src) {
			dst.Data.Strs[dstIdx] = src.Data.Strs[srcIdx]
		}
	case LX.T_BOOL:
		if src.Type == LX.T_BOOL {
			dst.Data.Bools[dstIdx] = src.Data.Bools[srcIdx]
		}
	}
}
