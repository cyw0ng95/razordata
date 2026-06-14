package ls

import (
	"github.com/cyw0ng95/razordata/internal/ENG/DP"
)

type Pair = dp.Pair
type KV = dp.KV

var (
	ErrEncodeRow   = dp.ErrEncodeRow
	ErrDecodeRow   = dp.ErrDecodeRow
	ErrEncodeBlock = dp.ErrEncodeBlock
	ErrDecodeBlock = dp.ErrDecodeBlock
)

func EncodeRow(row Row, schema *TableSchema) ([]byte, error) {
	return dp.EncodeRow(row, schema)
}

func DecodeRow(data []byte, schema *TableSchema) (Row, error) {
	return dp.DecodeRow(data, schema)
}

func EncodeBlock(kvs []Pair, restartInterval int) ([]byte, error) {
	return dp.EncodeBlock(kvs, restartInterval)
}

func DecodeBlock(data []byte) ([]KV, []int, error) {
	return dp.DecodeBlock(data)
}

func EncodeUint64(v uint64) []byte           { return dp.EncodeUint64(v) }
func DecodeUint64(data []byte) (uint64, int) { return dp.DecodeUint64(data) }
