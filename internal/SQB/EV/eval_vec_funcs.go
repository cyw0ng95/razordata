package EV

import (
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
		return fillLiteralColumn(batch, LX.T_TEXT, "0.26.7")
	case "SQLITE_SOURCE_ID":
		return fillLiteralColumn(batch, LX.T_TEXT, "razordata-v0.26.7")
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
