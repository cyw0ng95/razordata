package VL

import (
	"encoding/binary"
)

type WALRecord struct {
	Type     uint8
	TxnID    uint64
	CommitTS uint64
	KeyCount uint32
	Keys     [][]byte
}

func EncodeCommitRecord(txnID, commitTS uint64, keys [][]byte) []byte {
	keyCount := uint32(len(keys))

	var bufSize int
	bufSize = 1 + 8 + 8 + 4

	var totalKeyBytes int
	for _, k := range keys {
		keyLen := binary.MaxVarintLen64 + len(k)
		bufSize += keyLen
		totalKeyBytes += len(k)
	}

	buf := make([]byte, bufSize)
	off := 0

	buf[off] = WALRecordCommit
	off++

	binary.LittleEndian.PutUint64(buf[off:], txnID)
	off += 8

	binary.LittleEndian.PutUint64(buf[off:], commitTS)
	off += 8

	binary.LittleEndian.PutUint32(buf[off:], keyCount)
	off += 4

	for _, k := range keys {
		off += binary.PutUvarint(buf[off:], uint64(len(k)))
		copy(buf[off:], k)
		off += len(k)
	}

	return buf[:off]
}

func DecodeCommitRecord(data []byte) (*WALRecord, error) {
	if len(data) < 21 {
		return nil, ErrInvalidWALRecord
	}

	off := 0
	rec := &WALRecord{}

	rec.Type = data[off]
	off++

	rec.TxnID = binary.LittleEndian.Uint64(data[off:])
	off += 8

	rec.CommitTS = binary.LittleEndian.Uint64(data[off:])
	off += 8

	rec.KeyCount = binary.LittleEndian.Uint32(data[off:])
	off += 4

	for i := uint32(0); i < rec.KeyCount; i++ {
		if off >= len(data) {
			return nil, ErrInvalidWALRecord
		}
		keyLen, n := binary.Uvarint(data[off:])
		if n <= 0 || off+n+int(keyLen) > len(data) {
			return nil, ErrInvalidWALRecord
		}
		off += n
		key := make([]byte, keyLen)
		copy(key, data[off:off+int(keyLen)])
		rec.Keys = append(rec.Keys, key)
		off += int(keyLen)
	}

	return rec, nil
}
