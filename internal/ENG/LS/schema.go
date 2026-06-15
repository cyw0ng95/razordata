package ls

import "github.com/cyw0ng95/razordata/internal/ENG/SC"

type Row = sc.Row
type Validator = sc.Validator
type TableSchema = sc.TableSchema
type ColumnDef = sc.ColumnDef
type ColumnType = sc.ColumnType

const (
	CTInt       = sc.CTInt
	CTBigInt    = sc.CTBigInt
	CTVarchar   = sc.CTVarchar
	CTFloat     = sc.CTFloat
	CTBool      = sc.CTBool
	CTText      = sc.CTText
	CTBlob      = sc.CTBlob
	CTTimestamp = sc.CTTimestamp
)

var (
	ErrNullValue    = sc.ErrNullValue
	ErrTypeMismatch = sc.ErrTypeMismatch
	ErrConstraint   = sc.ErrConstraint
	ErrInvalidValue = sc.ErrInvalidValue
)

func NewValidator() *Validator { return sc.NewValidator() }

func EncodeInt(v int64) []byte                   { return sc.EncodeInt(v) }
func DecodeInt(data []byte) (int64, error)       { return sc.DecodeInt(data) }
func EncodeBigInt(v int64) []byte                { return sc.EncodeBigInt(v) }
func DecodeBigInt(data []byte) (int64, error)    { return sc.DecodeBigInt(data) }
func EncodeFloat(v float64) []byte               { return sc.EncodeFloat(v) }
func DecodeFloat(data []byte) (float64, error)   { return sc.DecodeFloat(data) }
func EncodeBool(v bool) []byte                   { return sc.EncodeBool(v) }
func DecodeBool(data []byte) (bool, error)       { return sc.DecodeBool(data) }
func EncodeVarchar(v string) []byte              { return sc.EncodeVarchar(v) }
func DecodeVarchar(data []byte) (string, error)  { return sc.DecodeVarchar(data) }
func EncodeText(v string) []byte                 { return sc.EncodeText(v) }
func DecodeText(data []byte) (string, error)     { return sc.DecodeText(data) }
func EncodeBlob(v []byte) []byte                 { return sc.EncodeBlob(v) }
func DecodeBlob(data []byte) ([]byte, error)     { return sc.DecodeBlob(data) }
func EncodeTimestamp(v int64) []byte             { return sc.EncodeTimestamp(v) }
func DecodeTimestamp(data []byte) (int64, error) { return sc.DecodeTimestamp(data) }
