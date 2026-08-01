package EV

import (
	CT "github.com/cyw0ng95/razordata/internal/SYS/CT"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

type Value = CT.Value
type ValueKind = CT.ValueKind
type Row = pl.Row
type Operator = DT.Operator
type ExecContext = pl.ExecContext
type ColInfo = pl.ColInfo

const (
	KindNull  = CT.KindNull
	KindInt   = CT.KindInt
	KindFloat = CT.KindFloat
	KindText  = CT.KindText
	KindBlob  = CT.KindBlob
	KindBool  = CT.KindBool
)

var ErrNoRows = pl.ErrNoRows
