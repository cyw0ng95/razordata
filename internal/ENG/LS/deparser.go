// Deprecated: deparser.go is a compatibility shim. The row and block
// encoders have moved to ENG/DP/ in iter-10 (Phase 0). This file
// re-exports the moved symbols under the ls. namespace so existing
// iter-09 callers and the test suite compile unchanged.
//
// The shim is removed in a follow-up commit (iter-10 close-out).
// After that, callers should import ENG/DP/ directly.
package ls

import (
	dp "github.com/cyw0ng95/razordata/internal/ENG/DP"
)

// Re-exported error sentinels from ENG/DP.
var (
	ErrEncodeRow   = dp.ErrEncodeRow
	ErrDecodeRow   = dp.ErrDecodeRow
	ErrEncodeBlock = dp.ErrEncodeBlock
	ErrDecodeBlock = dp.ErrDecodeBlock
)

// Re-exported pair/KV types from ENG/DP.
type (
	Pair = dp.Pair
	KV   = dp.KV
)

// EncodeRow serializes row per the schema; the shim delegates to
// dp.EncodeRow. See ENG/DP/row.go for the wire format.
func EncodeRow(row Row, schema *TableSchema) ([]byte, error) {
	return dp.EncodeRow(row, schema)
}

// DecodeRow is the inverse of EncodeRow.
func DecodeRow(data []byte, schema *TableSchema) (Row, error) {
	return dp.DecodeRow(data, schema)
}

// EncodeBlock serializes a list of pairs into a block. See
// ENG/DP/block.go for the block format.
func EncodeBlock(pairs []Pair, restartInterval int) ([]byte, error) {
	return dp.EncodeBlock(pairs, restartInterval)
}

// DecodeBlock is the inverse of EncodeBlock.
func DecodeBlock(data []byte) ([]KV, []int, error) {
	return dp.DecodeBlock(data)
}
