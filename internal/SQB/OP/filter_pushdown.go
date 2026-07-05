package OP

import (
	"encoding/binary"
	"math"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// rowValueType tags — mirror of SQB/DT/storage.go constants for raw-byte eval.
const (
	rvNull  byte = 0
	rvInt   byte = 1
	rvStr   byte = 2
	rvBool  byte = 3
	rvFloat byte = 4
	rvBytes byte = 5
)

// CanEvaluateOnRaw checks if a predicate can be evaluated on raw encoded
// bytes without decoding the row. REQ001225.
func CanEvaluateOnRaw(pred PS.Expr, schema *DT.StoreSchema) bool {
	if schema == nil {
		return false
	}
	bin, ok := pred.(*PS.BinaryExpr)
	if !ok {
		return false
	}
	if !isRawByteComparisonOp(bin.Op) {
		return false
	}
	col, lit := extractColumnAndLiteral(bin)
	if col == nil || lit == nil {
		return false
	}
	idx := colSlotIdx(col)
	return canEvaluateCol(schema, idx)
}

// CompileRawByteFilter creates a raw-byte filter function from a predicate.
// REQ001225. Returns nil if the predicate cannot be evaluated on raw bytes.
func CompileRawByteFilter(pred PS.Expr, schema *DT.StoreSchema) func([]byte) bool {
	if !CanEvaluateOnRaw(pred, schema) {
		return nil
	}
	bin := pred.(*PS.BinaryExpr)
	col, lit := extractColumnAndLiteral(bin)
	idx := colSlotIdx(col)
	colType := schema.ColTypes[idx]
	op := bin.Op
	switch litType := lit.(type) {
	case *PS.NumberLiteral:
		switch colType {
		case LX.T_INT:
			return func(data []byte) bool {
				return evalIntOnRaw(data, idx, op, litType.Val)
			}
		case LX.T_FLOAT:
			return func(data []byte) bool {
				return evalFloatOnRaw(data, idx, op, float64(litType.Val))
			}
		}
	case *PS.FloatLiteral:
		switch colType {
		case LX.T_FLOAT:
			return func(data []byte) bool {
				return evalFloatOnRaw(data, idx, op, litType.Val)
			}
		case LX.T_INT:
			return func(data []byte) bool {
				return evalIntOnRaw(data, idx, op, int64(litType.Val))
			}
		}
	case *PS.StringLiteral:
		return func(data []byte) bool {
			return evalStrOnRaw(data, idx, op, litType.Val)
		}
	case *PS.BoolLiteral:
		return func(data []byte) bool {
			return evalBoolOnRaw(data, idx, op, litType.Val)
		}
	}
	return nil
}

func isRawByteComparisonOp(op LX.TokenType) bool {
	switch op {
	case LX.T_EQ, LX.T_NE, LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
		return true
	default:
		return false
	}
}

func extractColumnAndLiteral(bin *PS.BinaryExpr) (colExpr, litExpr PS.Expr) {
	leftCol, leftLit := columnAndLiteral(bin.Left)
	rightCol, rightLit := columnAndLiteral(bin.Right)
	if leftCol != nil && rightLit != nil {
		return leftCol, rightLit
	}
	if rightCol != nil && leftLit != nil {
		return rightCol, leftLit
	}
	return nil, nil
}

func columnAndLiteral(e PS.Expr) (col, lit PS.Expr) {
	if ident, ok := e.(*PS.Ident); ok {
		return ident, nil
	}
	if qname, ok := e.(*PS.QualifiedName); ok {
		return qname, nil
	}
	if isLiteral(e) {
		return nil, e
	}
	return nil, nil
}

func isLiteral(e PS.Expr) bool {
	switch e.(type) {
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral, *PS.BoolLiteral:
		return true
	default:
		return false
	}
}

func colSlotIdx(col PS.Expr) int {
	switch v := col.(type) {
	case *PS.Ident:
		return v.SlotIdx
	case *PS.QualifiedName:
		return v.SlotIdx
	}
	return -1
}

func canEvaluateCol(schema *DT.StoreSchema, idx int) bool {
	if idx < 0 || idx >= len(schema.Cols) {
		return false
	}
	t := schema.ColTypes[idx]
	return t == LX.T_INT || t == LX.T_FLOAT || t == LX.T_BOOL || t == LX.T_TEXT || t == LX.T_STRING || t == LX.T_BLOB
}

func skipToCol(data []byte, colIdx int) int {
	off := 0
	if len(data) == 0 {
		return -1
	}
	_, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return -1
	}
	off += n

	for i := 0; i < colIdx; i++ {
		if off >= len(data) {
			return -1
		}
		tag := data[off]
		off++
		switch tag {
		case rvInt, rvFloat:
			off += 8
		case rvBool:
			off++
		case rvNull:
		case rvStr, rvBytes:
			if off >= len(data) {
				return -1
			}
			l, n2 := binary.Uvarint(data[off:])
			if n2 <= 0 {
				return -1
			}
			off += n2 + int(l)
		default:
			return -1
		}
	}
	return off
}

func evalIntOnRaw(data []byte, colIdx int, op LX.TokenType, literal int64) bool {
	off := skipToCol(data, colIdx)
	if off < 0 || off >= len(data) {
		return false
	}
	tag := data[off]
	off++
	if tag == rvNull {
		return false
	}
	if tag != rvInt {
		return false
	}
	if off+8 > len(data) {
		return false
	}
	val := int64(binary.BigEndian.Uint64(data[off : off+8]))
	return compareInt64(val, literal, op)
}

func evalFloatOnRaw(data []byte, colIdx int, op LX.TokenType, literal float64) bool {
	off := skipToCol(data, colIdx)
	if off < 0 || off >= len(data) {
		return false
	}
	tag := data[off]
	off++
	if tag == rvNull {
		return false
	}
	if tag != rvFloat {
		return false
	}
	if off+8 > len(data) {
		return false
	}
	val := math.Float64frombits(binary.BigEndian.Uint64(data[off : off+8]))
	return compareFloat64(val, literal, op)
}

func evalStrOnRaw(data []byte, colIdx int, op LX.TokenType, literal string) bool {
	off := skipToCol(data, colIdx)
	if off < 0 || off >= len(data) {
		return false
	}
	tag := data[off]
	off++
	if tag == rvNull {
		return false
	}
	if tag != rvStr {
		return false
	}
	if off >= len(data) {
		return false
	}
	l, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return false
	}
	valStart := off + n
	valEnd := valStart + int(l)
	if valEnd > len(data) {
		return false
	}
	val := string(data[valStart:valEnd])
	return compareStrings(val, literal, op)
}

func evalBoolOnRaw(data []byte, colIdx int, op LX.TokenType, literal bool) bool {
	off := skipToCol(data, colIdx)
	if off < 0 || off >= len(data) {
		return false
	}
	tag := data[off]
	off++
	if tag == rvNull {
		return false
	}
	if tag != rvBool {
		return false
	}
	if off >= len(data) {
		return false
	}
	val := data[off] != 0
	return compareBools(val, literal, op)
}

func compareInt64(a, b int64, op LX.TokenType) bool {
	switch op {
	case LX.T_EQ:
		return a == b
	case LX.T_NE:
		return a != b
	case LX.T_LT:
		return a < b
	case LX.T_LE:
		return a <= b
	case LX.T_GT:
		return a > b
	case LX.T_GE:
		return a >= b
	}
	return false
}

func compareFloat64(a, b float64, op LX.TokenType) bool {
	switch op {
	case LX.T_EQ:
		return a == b
	case LX.T_NE:
		return a != b
	case LX.T_LT:
		return a < b
	case LX.T_LE:
		return a <= b
	case LX.T_GT:
		return a > b
	case LX.T_GE:
		return a >= b
	}
	return false
}

func compareStrings(a, b string, op LX.TokenType) bool {
	switch op {
	case LX.T_EQ:
		return a == b
	case LX.T_NE:
		return a != b
	case LX.T_LT:
		return a < b
	case LX.T_LE:
		return a <= b
	case LX.T_GT:
		return a > b
	case LX.T_GE:
		return a >= b
	}
	return false
}

func compareBools(a, b bool, op LX.TokenType) bool {
	switch op {
	case LX.T_EQ:
		return a == b
	case LX.T_NE:
		return a != b
	default:
		return false
	}
}
