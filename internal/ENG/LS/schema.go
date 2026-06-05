// Deprecated: schema.go is a compatibility shim. The schema types,
// validator, and value codecs have moved to ENG/SC/ and ENG/DP/ in
// iter-10 (Phase 0). This file re-exports the moved symbols under
// the ls. namespace so existing iter-09 callers and the test suite
// compile unchanged.
//
// The shim is removed in a follow-up commit (iter-10 close-out).
// After that, callers should import ENG/SC/ and ENG/DP/ directly.
package ls

import (
	dp "github.com/cyw0ng95/razordata/internal/ENG/DP"
	sc "github.com/cyw0ng95/razordata/internal/ENG/SC"
)

// Re-exported types from ENG/SC.
type (
	Row         = sc.Row
	TableSchema = sc.TableSchema
	ColumnDef   = sc.ColumnDef
	ColumnType  = sc.ColumnType
	Validator   = sc.Validator
)

// Re-exported error sentinels from ENG/SC.
var (
	ErrNullValue    = sc.ErrNullValue
	ErrTypeMismatch = sc.ErrTypeMismatch
	ErrConstraint   = sc.ErrConstraint
	ErrInvalidValue = sc.ErrInvalidValue
)

// NewValidator returns a ready-to-use validator; the shim delegates
// to sc.NewValidator.
func NewValidator() *Validator { return sc.NewValidator() }

// Re-exported ColumnType constants from ENG/SC.
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

// Re-exported value codecs from ENG/DP.
func EncodeInt(v int64) []byte                  { return dp.EncodeInt(v) }
func DecodeInt(data []byte) (int64, error)      { return dp.DecodeInt(data) }
func EncodeBigInt(v int64) []byte               { return dp.EncodeBigInt(v) }
func DecodeBigInt(data []byte) (int64, error)   { return dp.DecodeBigInt(data) }
func EncodeFloat(v float64) []byte              { return dp.EncodeFloat(v) }
func DecodeFloat(data []byte) (float64, error)  { return dp.DecodeFloat(data) }
func EncodeBool(v bool) []byte                  { return dp.EncodeBool(v) }
func DecodeBool(data []byte) (bool, error)      { return dp.DecodeBool(data) }
func EncodeVarchar(v string) []byte             { return dp.EncodeVarchar(v) }
func DecodeVarchar(data []byte) (string, error) { return dp.DecodeVarchar(data) }
func EncodeText(v string) []byte                { return dp.EncodeText(v) }
func DecodeText(data []byte) (string, error)    { return dp.DecodeText(data) }
func EncodeBlob(v []byte) []byte                { return dp.EncodeBlob(v) }
func DecodeBlob(data []byte) ([]byte, error)    { return dp.DecodeBlob(data) }
func EncodeTimestamp(v int64) []byte            { return dp.EncodeTimestamp(v) }
func DecodeTimestamp(data []byte) (int64, error) {
	return dp.DecodeTimestamp(data)
}
