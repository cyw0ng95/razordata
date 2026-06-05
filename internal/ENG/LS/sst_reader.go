package ls

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

var (
	ErrKeyNotFound = errors.New("key not found")
	ErrBloomMiss   = errors.New("bloom filter miss")
)

type sstReader struct {
	data       []byte
	indexBlock []indexEntry
	bloom      []byte
}

func openSST(data []byte) (*sstReader, error) {
	if len(data) < 32 {
		return nil, ErrInvalidSSTFormat
	}

	r := &sstReader{data: data}

	footerStart := len(data) - 28
	indexOffset := binary.LittleEndian.Uint64(data[footerStart:])
	indexSize := binary.LittleEndian.Uint32(data[footerStart+8:])
	bloomOffset := binary.LittleEndian.Uint64(data[footerStart+12:])
	bloomSize := binary.LittleEndian.Uint32(data[footerStart+20:])
	magic := binary.LittleEndian.Uint32(data[footerStart+24:])

	if magic != sstMagic {
		return nil, ErrInvalidSSTFormat
	}

	if indexOffset > 0 && indexOffset < uint64(len(data)) {
		indexData := data[indexOffset : indexOffset+uint64(indexSize)]
		r.indexBlock = parseIndexBlock(indexData)
	}

	if bloomOffset > 0 && bloomOffset < uint64(len(data)) {
		r.bloom = data[bloomOffset : bloomOffset+uint64(bloomSize)]
	}

	return r, nil
}

func parseIndexBlock(data []byte) []indexEntry {
	var entries []indexEntry
	offset := 0

	for offset < len(data) {
		if offset+4 > len(data) {
			break
		}
		keyLen, n := decodeVarint(data[offset:])
		offset += n

		if offset+int(keyLen) > len(data) {
			break
		}
		key := data[offset : offset+int(keyLen)]
		offset += int(keyLen)

		if offset+16 > len(data) {
			break
		}
		blockOffset := binary.LittleEndian.Uint64(data[offset:])
		offset += 8
		blockSize := binary.LittleEndian.Uint64(data[offset:])
		offset += 8

		entries = append(entries, indexEntry{
			largestKey:  key,
			blockOffset: int(blockOffset),
			blockSize:   int(blockSize),
		})
	}

	return entries
}

func (r *sstReader) mayContain(key []byte) bool {
	if len(r.bloom) == 0 {
		return true
	}

	hash1 := crc32.Checksum(key, crc32.MakeTable(crc32.Koopman))
	hash2 := crc32.Checksum(key, crc32.MakeTable(crc32.Castagnoli))

	size := len(r.bloom) * 8
	bucket1 := int(hash1) % size
	bucket2 := int(hash2) % size

	return (r.bloom[bucket1/8]&(1<<(bucket1%8)) != 0) &&
		(r.bloom[bucket2/8]&(1<<(bucket2%8)) != 0)
}

func (r *sstReader) Find(key []byte) ([]byte, bool) {
	if !r.mayContain(key) {
		return nil, false
	}

	blockIdx := r.searchIndex(key)
	if blockIdx < 0 || blockIdx >= len(r.indexBlock) {
		return nil, false
	}

	blockData := r.readBlock(r.indexBlock[blockIdx].blockOffset, r.indexBlock[blockIdx].blockSize)
	if blockData == nil {
		return nil, false
	}

	kvPairs, err := decodeBlock(blockData)
	if err != nil {
		return nil, false
	}

	for _, kv := range kvPairs {
		cmp := bytes.Compare(kv.key, key)
		if cmp == 0 {
			return kv.value, true
		}
		if cmp > 0 {
			break
		}
	}

	return nil, false
}

func (r *sstReader) searchIndex(key []byte) int {
	if len(r.indexBlock) == 0 {
		return -1
	}

	lo, hi := 0, len(r.indexBlock)-1

	for lo <= hi {
		mid := (lo + hi) / 2
		cmp := bytes.Compare(r.indexBlock[mid].largestKey, key)
		if cmp >= 0 {
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}

	if lo >= len(r.indexBlock) {
		return len(r.indexBlock) - 1
	}

	if lo < 0 {
		return 0
	}

	return lo
}

func (r *sstReader) readBlock(offset, size int) []byte {
	if offset < 0 || offset >= len(r.data) {
		return nil
	}

	end := offset + size
	if end > len(r.data) {
		end = len(r.data)
	}

	return r.data[offset:end]
}

type kvPair struct {
	key   []byte
	value []byte
}

func decodeBlock(data []byte) ([]kvPair, error) {
	if len(data) < 8 {
		return nil, ErrInvalidSSTFormat
	}

	checksum := binary.LittleEndian.Uint32(data[len(data)-4:])
	blockData := data[:len(data)-8]

	computedChecksum := crc32.Checksum(blockData, crc32.MakeTable(crc32.Koopman))
	if computedChecksum != checksum {
		return nil, ErrInvalidSSTFormat
	}

	restartCountPos := len(data) - 8
	restartCount := int(binary.LittleEndian.Uint32(data[restartCountPos:]))
	restartOffset := len(data) - 8 - restartCount*4

	if restartOffset < 0 || restartOffset > len(data)-8 {
		return nil, ErrInvalidSSTFormat
	}

	restartPoints := make([]int, restartCount)
	for i := 0; i < restartCount; i++ {
		if restartOffset+i*4+4 <= len(data)-4 {
			restartPoints[i] = int(binary.LittleEndian.Uint32(data[restartOffset+i*4:]))
		}
	}

	var pairs []kvPair

	for i, restartPos := range restartPoints {
		endPos := len(blockData)
		if i+1 < len(restartPoints) {
			endPos = restartPoints[i+1]
		}

		pos := restartPos
		for pos < endPos && pos < len(blockData) {
			if pos >= len(blockData) {
				break
			}
			keyLen, n := decodeVarint(blockData[pos:])
			pos += n

			if pos >= len(blockData) {
				break
			}

			valueLen, n := decodeVarint(blockData[pos:])
			pos += n

			if pos+int(keyLen) > len(blockData) {
				break
			}
			key := blockData[pos : pos+int(keyLen)]
			pos += int(keyLen)

			if pos+int(valueLen) > len(blockData) {
				break
			}
			value := blockData[pos : pos+int(valueLen)]
			pos += int(valueLen)

			pairs = append(pairs, kvPair{key: append([]byte(nil), key...), value: append([]byte(nil), value...)})
		}
	}

	return pairs, nil
}

type sstIterator struct {
	reader  *sstReader
	current int
	pairs   []kvPair
}

func (r *sstReader) Iterator() *sstIterator {
	return &sstIterator{
		reader:  r,
		current: 0,
		pairs:   nil,
	}
}

func (it *sstIterator) Next() bool {
	if it.pairs == nil || it.current >= len(it.pairs) {
		if it.current > 0 && it.reader != nil && it.current < len(it.reader.indexBlock) {
			blockData := it.reader.readBlock(it.reader.indexBlock[it.current].blockOffset, it.reader.indexBlock[it.current].blockSize)
			var err error
			it.pairs, err = decodeBlock(blockData)
			if err != nil {
				return false
			}
			it.current = 0
			return len(it.pairs) > 0
		}
		return false
	}
	it.current++
	return it.current < len(it.pairs)
}

func (it *sstIterator) Key() []byte {
	if it.pairs == nil || it.current >= len(it.pairs) {
		return nil
	}
	return it.pairs[it.current].key
}

func (it *sstIterator) Value() []byte {
	if it.pairs == nil || it.current >= len(it.pairs) {
		return nil
	}
	return it.pairs[it.current].value
}

func (it *sstIterator) Close() error {
	return nil
}

func (it *sstIterator) Err() error {
	return nil
}

var _ io.Closer = (*sstReader)(nil)

func (r *sstReader) Close() error {
	return nil
}
