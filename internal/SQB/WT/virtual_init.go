package WT

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
)

func init() {
	DT.EvalVirtualColumn = evalVirtualColumn
}

func evalVirtualColumn(schema *DT.StoreSchema, idx int, row *DT.Row) DT.Value {
	if schema.Generated != nil && idx < len(schema.Generated) && schema.Generated[idx] != nil {
		v, err := EV.EvalValue(schema.Generated[idx], row, nil)
		if err == nil {
			return v
		}
	}
	return DT.NullValue()
}
