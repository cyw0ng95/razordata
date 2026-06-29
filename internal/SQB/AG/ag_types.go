package AG

import DT "github.com/cyw0ng95/razordata/internal/SQB/DT"

type Value = DT.Value
type ValueKind = DT.ValueKind
type Row = DT.Row
type Operator = DT.Operator
type ExecContext = DT.ExecContext
type ColInfo = DT.ColInfo

const (
	KindNull  = DT.KindNull
	KindInt   = DT.KindInt
	KindFloat = DT.KindFloat
	KindText  = DT.KindText
	KindBlob  = DT.KindBlob
	KindBool  = DT.KindBool
)

var ErrNoRows = DT.ErrNoRows
var ErrClosed = DT.ErrClosed
