package AG

import (
	CT "github.com/cyw0ng95/razordata/internal/SYS/CT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

type Value = CT.Value
type ValueKind = CT.ValueKind
type Row = pl.Row
type Operator = pl.Operator
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
