package OP

import (
	"encoding/binary"
	"fmt"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
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

// coveringAppendToBatch decodes one index entry's value and primary key
// directly into a batch at the given row index, bypassing the per-row
// Row allocation. Maps index column names to batch column indices via
// colMap. REQ001479.
func coveringAppendToBatch(schema *StoreSchema, idxCols []string, idxTypes []LX.TokenType, idxVal, pkBytes []byte, pkCol string, batch *UT.Batch, rowIdx int) error {
	rem := idxVal
	for k, c := range idxCols {
		v, next, ok := coveringSplitIndexValue(rem, idxTypes[k])
		if !ok {
			return fmt.Errorf("OP: covering batch underflow on column %s", c)
		}
		// Find the batch column index for this index column.
		colIdx := -1
		for j := 0; j < len(batch.Cols) && colIdx < 0; j++ {
			if batch.Cols[j].Name == c {
				colIdx = j
			}
		}
		if colIdx < 0 {
			rem = next
			continue
		}
		// Write value directly to batch column data.
		col := &batch.Cols[colIdx]
		t := idxTypes[k]
		switch {
		case isCoveringIntType(t):
			if len(v) >= 8 {
				if col.Data.Ints == nil {
					col.Data.Ints = UT.PoolGetInts(colIdx, UT.BatchSize)
				}
				if rowIdx < len(col.Data.Ints) {
					col.Data.Ints[rowIdx] = int64(binary.BigEndian.Uint64(v))
				}
			}
		case t == LX.T_BLOB:
			if col.Data.Strs == nil {
				col.Data.Strs = UT.PoolGetStrs(colIdx, UT.BatchSize)
			}
			if rowIdx < len(col.Data.Strs) {
				col.Data.Strs[rowIdx] = string(v)
			}
		case t == LX.T_BOOL:
			if col.Data.Bools == nil {
				col.Data.Bools = UT.PoolGetBools(colIdx, UT.BatchSize)
			}
			if rowIdx < len(col.Data.Bools) {
				col.Data.Bools[rowIdx] = len(v) > 0 && v[0] != 0
			}
		default:
			// TEXT / VARCHAR
			if col.Data.Strs == nil {
				col.Data.Strs = UT.PoolGetStrs(colIdx, UT.BatchSize)
			}
			if rowIdx < len(col.Data.Strs) {
				col.Data.Strs[rowIdx] = string(v)
			}
		}
		rem = next
	}
	if pkCol != "" {
		colIdx := -1
		for j := 0; j < len(batch.Cols); j++ {
			if batch.Cols[j].Name == pkCol {
				colIdx = j
				break
			}
		}
		if colIdx >= 0 {
			col := &batch.Cols[colIdx]
			pkType := LX.T_INT_KW
			// Check the schema for a more specific type.
			if schema != nil {
				for si, sn := range schema.Cols {
					if sn == pkCol && si < len(schema.ColTypes) {
						pkType = schema.ColTypes[si]
						break
					}
				}
			}
			switch {
			case isCoveringIntType(pkType):
				if len(pkBytes) >= 8 {
					if col.Data.Ints == nil {
						col.Data.Ints = UT.PoolGetInts(colIdx, UT.BatchSize)
					}
					if rowIdx < len(col.Data.Ints) {
						col.Data.Ints[rowIdx] = int64(binary.BigEndian.Uint64(pkBytes))
					}
				}
			case pkType == LX.T_BLOB:
				if col.Data.Strs == nil {
					col.Data.Strs = UT.PoolGetStrs(colIdx, UT.BatchSize)
				}
				if rowIdx < len(col.Data.Strs) {
					col.Data.Strs[rowIdx] = string(pkBytes)
				}
			default:
				if col.Data.Strs == nil {
					col.Data.Strs = UT.PoolGetStrs(colIdx, UT.BatchSize)
				}
				if rowIdx < len(col.Data.Strs) {
					col.Data.Strs[rowIdx] = string(pkBytes)
				}
			}
		}
	}
	return nil
}
