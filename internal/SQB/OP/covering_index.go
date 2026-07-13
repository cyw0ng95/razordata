package OP

import (
	"encoding/binary"
	"fmt"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

func isCoveringIntType(t LX.TokenType) bool {
	switch t {
	case LX.T_INT_KW, LX.T_BIGINT, LX.T_NUMERIC, LX.T_TIMESTAMP, LX.T_DATE, LX.T_TIME:
		return true
	}
	return false
}

func coveringSplitIndexValue(raw []byte, t LX.TokenType) (val, rest []byte, ok bool) {
	if isCoveringIntType(t) {
		if len(raw) < 8 {
			return nil, raw, false
		}
		return raw[:8], raw[8:], true
	}
	end := 0
	for end < len(raw) && raw[end] != 0x00 {
		end++
	}
	if end >= len(raw) {
		return raw, nil, true
	}
	return raw[:end], raw[end+1:], true
}

func coveringRawToValue(raw []byte, t LX.TokenType) Value {
	if isCoveringIntType(t) {
		if len(raw) < 8 {
			return Value{}
		}
		return DT.NewIntValue(int64(binary.BigEndian.Uint64(raw)))
	}
	switch t {
	case LX.T_BLOB:
		return DT.NewBlobValue(raw)
	case LX.T_BOOL:
		return DT.NewBoolValue(len(raw) > 0 && raw[0] != 0)
	}
	return DT.NewTextValue(string(raw))
}

func coveringBuildRow(schema *StoreSchema, table string, idxCols []string, idxTypes []LX.TokenType, idxVal, pkBytes []byte, pkCol string) (Row, error) {
	if schema == nil {
		return Row{}, fmt.Errorf("OP: covering scan on %s has no schema", table)
	}
	row := Row{
		Cols:     schema.Cols,
		Types:    schema.ColTypes,
		Data:     make([]Value, len(schema.Cols)),
		ColIndex: schema.ColIndex,
	}
	row.TableName = table
	rem := idxVal
	for k, c := range idxCols {
		v, next, ok := coveringSplitIndexValue(rem, idxTypes[k])
		if !ok {
			return Row{}, fmt.Errorf("OP: covering index value underflow on column %s", c)
		}
		pos, exists := schema.ColIndex[c]
		if !exists {
			return Row{}, fmt.Errorf("OP: covering index column %s missing from schema", c)
		}
		row.Data[pos] = coveringRawToValue(v, idxTypes[k])
		rem = next
	}
	if pkCol != "" {
		pos, exists := schema.ColIndex[pkCol]
		if !exists {
			return Row{}, fmt.Errorf("OP: covering PK column %s missing from schema", pkCol)
		}
		pkType := LX.T_INT_KW
		if pos < len(schema.ColTypes) {
			pkType = schema.ColTypes[pos]
		}
		row.Data[pos] = coveringRawToValue(pkBytes, pkType)
	}
	return row, nil
}
