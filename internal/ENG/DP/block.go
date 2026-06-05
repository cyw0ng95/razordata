// Package DP is the deparser cluster for the storage engine.
//
// This file (block.go) owns the SST data-block encoding. A block is
// a sequence of (key, value) pairs prefixed by their unsigned-varint
// lengths, followed by a restart array and a 4-byte restart count.
//
// The block format is independent of the row format (row.go). The
// row format is the schema-aware in-memory encoding; the block
// format is the on-disk / over-the-wire encoding of arbitrary
// key-value pairs. They share encodeUint64/decodeUint64 for length
// framing.
//
// This file was moved from ENG/LS/deparser.go in iter-10 (Phase 0).
// Behavior is unchanged from the original v1 implementation; only
// the package path differs.
package dp

import (
	"encoding/binary"
	"errors"
)

var (
	ErrEncodeBlock = errors.New("failed to encode block")
	ErrDecodeBlock = errors.New("failed to decode block")
)

// EncodeBlock serializes a list of pairs into a block. A restart
// point is recorded every restartInterval pairs; restart points are
// absolute byte offsets into the encoded block. A non-positive
// restartInterval is normalized to 1 (every pair is a restart).
// An empty input returns (nil, nil) — the empty block is the
// identity element for concatenation.
func EncodeBlock(kvs []Pair, restartInterval int) ([]byte, error) {
	if len(kvs) == 0 {
		return nil, nil
	}

	if restartInterval <= 0 {
		restartInterval = 1
	}

	var buf []byte

	restarts := make([]int, 0, len(kvs)/restartInterval+1)

	for i, kv := range kvs {
		keyLen := encodeUint64(uint64(len(kv.Key)))
		valLen := encodeUint64(uint64(len(kv.Value)))

		buf = append(buf, keyLen...)
		buf = append(buf, kv.Key...)
		buf = append(buf, valLen...)
		buf = append(buf, kv.Value...)

		if (i+1)%restartInterval == 0 {
			restarts = append(restarts, len(buf))
		}
	}

	restarts = append(restarts, len(buf))

	for _, r := range restarts {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], uint32(r))
		buf = append(buf, b[:]...)
	}

	numRestarts := make([]byte, 4)
	binary.LittleEndian.PutUint32(numRestarts, uint32(len(restarts)))
	buf = append(buf, numRestarts...)

	return buf, nil
}

// DecodeBlock is the inverse of EncodeBlock. The returned []int is
// the restart point array (byte offsets into the data portion, not
// including the trailing restart array). An empty input decodes
// successfully to (nil, nil). Truncated restart headers, restart
// counts that don't match the block length, or key/value length
// varints that overflow the block all return ErrDecodeBlock.
func DecodeBlock(data []byte) ([]KV, []int, error) {
	if len(data) == 0 {
		return nil, nil, nil
	}

	if len(data) < 4 {
		return nil, nil, ErrDecodeBlock
	}

	numRestarts := binary.LittleEndian.Uint32(data[len(data)-4:])

	restartOffset := len(data) - 4 - int(numRestarts)*4
	if restartOffset < 0 {
		return nil, nil, ErrDecodeBlock
	}

	restarts := make([]int, numRestarts)
	for i := uint32(0); i < numRestarts; i++ {
		restarts[i] = int(binary.LittleEndian.Uint32(data[restartOffset+int(i)*4:]))
	}

	var kvs []KV
	offset := 0

	for offset < restartOffset {
		keyLen, n := decodeUint64(data[offset:])
		offset += n

		if offset+int(keyLen) > restartOffset {
			return nil, nil, ErrDecodeBlock
		}
		key := data[offset : offset+int(keyLen)]
		offset += int(keyLen)

		valLen, n := decodeUint64(data[offset:])
		offset += n

		if offset+int(valLen) > restartOffset {
			return nil, nil, ErrDecodeBlock
		}
		val := data[offset : offset+int(valLen)]
		offset += int(valLen)

		kvs = append(kvs, KV{Key: key, Value: val})
	}

	return kvs, restarts, nil
}

// encodeUint64 writes v as an unsigned varint (1 byte for v < 128,
// 2 bytes for v < 16384, etc., up to 10 bytes for the full uint64
// range). Used for length framing in row and block encodings.
func encodeUint64(v uint64) []byte {
	var buf [10]byte
	n := 0
	for v >= 0x80 {
		buf[n] = byte(v) | 0x80
		v >>= 7
		n++
	}
	buf[n] = byte(v)
	n++
	return buf[:n]
}

// decodeUint64 is the inverse of encodeUint64. It returns the value
// and the number of bytes consumed. A malformed varint (no
// terminating byte) returns (0, 0).
func decodeUint64(data []byte) (uint64, int) {
	var v uint64
	var shift int
	for i, b := range data {
		if b < 0x80 {
			v |= uint64(b) << shift
			return v, i + 1
		}
		v |= uint64(b&0x7F) << shift
		shift += 7
	}
	return 0, 0
}
