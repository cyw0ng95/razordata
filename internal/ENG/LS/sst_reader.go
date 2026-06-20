package ls

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"unsafe"
)

type sstReader struct {
	data        []byte
	indexBlock  []indexEntry
	bloom       []byte
	prefixBloom []byte
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

	dataLen := uint64(len(data))
	if indexOffset > 0 {
		if indexOffset >= dataLen || indexOffset+uint64(indexSize) > dataLen {
			return nil, ErrInvalidSSTFormat
		}
		indexData := data[indexOffset : indexOffset+uint64(indexSize)]
		r.indexBlock = parseIndexBlock(indexData)
	}

	if bloomOffset > 0 {
		if bloomOffset >= dataLen || bloomOffset+uint64(bloomSize) > dataLen {
			return nil, ErrInvalidSSTFormat
		}
		if indexOffset > 0 && indexOffset+uint64(indexSize) > bloomOffset {
			// index must come before bloom in the file
			return nil, ErrInvalidSSTFormat
		}
		r.bloom = data[bloomOffset : bloomOffset+uint64(bloomSize)]
		// REQ000047: prefix bloom is stored right after the regular bloom
		prefixBloomStart := bloomOffset + uint64(bloomSize)
		if prefixBloomStart < dataLen {
			remaining := dataLen - prefixBloomStart - 28 // subtract footer
			if remaining > 0 && remaining < dataLen && prefixBloomStart+remaining <= dataLen {
				r.prefixBloom = data[prefixBloomStart : prefixBloomStart+remaining]
			}
		}
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

	h1 := fnv1aHash(key, fnv1aOffset32)
	h2 := fnv1aHash(key, fnv1aPrime32)

	mask := uint32(len(r.bloom)*8) - 1
	bucket1 := int(h1 & mask)
	bucket2 := int(h2 & mask)

	return (r.bloom[bucket1/8]&(1<<(bucket1%8)) != 0) &&
		(r.bloom[bucket2/8]&(1<<(bucket2%8)) != 0)
}

// MayContainPrefix checks if the SST might contain a key with the given prefix.
// REQ000047, REQ000599: modulus is byte count, not bit count.
func (r *sstReader) MayContainPrefix(prefix []byte) bool {
	if len(r.prefixBloom) == 0 {
		return true
	}
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	mask := uint32(len(r.prefixBloom)) - 1
	h1 := fnv1aHash(prefix, fnv1aOffset32)
	h2 := fnv1aHash(prefix, fnv1aPrime32)
	bucket1 := int(h1 & mask)
	bucket2 := int(h2 & mask)
	return (r.prefixBloom[bucket1/8]&(1<<(bucket1%8)) != 0) &&
		(r.prefixBloom[bucket2/8]&(1<<(bucket2%8)) != 0)
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

	raw := r.data[offset:end]
	decompressed, err := decompressBlockDict(raw)
	if err != nil {
		if d2, err2 := decompressBlock(raw); err2 == nil {
			return d2
		}
		return raw // fallback to raw data
	}
	return decompressed
}

type kvPair struct {
	// key and value are borrowed pointers into the SST data
	// They point directly into r.data without copying
	key   []byte
	value []byte
}

// kvPairFromData creates a kvPair with borrowed pointers.
// The key and value slices point directly into the data buffer.
func kvPairFromData(data []byte, keyOffset, keyLen int, valOffset, valLen int) kvPair {
	if keyLen < 0 || valLen < 0 {
		return kvPair{}
	}
	// Use unsafe.Pointer arithmetic to create slices without copying
	keyPtr := unsafe.Pointer(&data[keyOffset])
	valPtr := unsafe.Pointer(&data[valOffset])
	return kvPair{
		key:   unsafe.Slice((*byte)(keyPtr), keyLen),
		value: unsafe.Slice((*byte)(valPtr), valLen),
	}
}

func decodeBlock(data []byte) ([]kvPair, error) {
	if len(data) < 8 {
		return nil, ErrInvalidSSTFormat
	}

	checksum := binary.LittleEndian.Uint32(data[len(data)-4:])
	blockData := data[:len(data)-8]

	computedChecksum := crc32.Checksum(blockData, crc32Koopman)
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
	for i := range restartCount {
		if restartOffset+i*4+4 <= len(data)-4 {
			restartPoints[i] = int(binary.LittleEndian.Uint32(data[restartOffset+i*4:]))
		}
	}

	if restartCount == 0 {
		restartPoints = append(restartPoints, 0)
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
			keyLen, keyVarintLen := decodeVarint(blockData[pos:])
			pos += keyVarintLen

			if pos+int(keyLen) > len(blockData) {
				break
			}

			valueLen, valVarintLen := decodeVarint(blockData[pos+int(keyLen):])

			// Borrow pointers directly into blockData (zero-copy)
			// key starts at pos (after keyLen varint)
			// value starts after key and valueLen varint
			keyOffset := pos
			valOffset := pos + int(keyLen) + valVarintLen
			pairs = append(pairs, kvPairFromData(
				blockData,
				keyOffset, int(keyLen),
				valOffset, int(valueLen),
			))

			// Advance past key, valueLen varint, and value
			pos = valOffset + int(valueLen)
		}
	}

	return pairs, nil
}

type sstIterator struct {
	reader   *sstReader
	blockIdx int      // current block index in reader.indexBlock
	pairIdx  int      // current pair index within the loaded block
	pairs    []kvPair // pairs for the current block; nil until first block is loaded
}

func (r *sstReader) Iterator() *sstIterator {
	return &sstIterator{
		reader:   r,
		blockIdx: -1, // sentinel: no block loaded yet
		pairIdx:  0,
		pairs:    nil,
	}
}

func (it *sstIterator) loadBlock(blockIdx int) bool {
	if it.reader == nil || blockIdx < 0 || blockIdx >= len(it.reader.indexBlock) {
		return false
	}
	entry := it.reader.indexBlock[blockIdx]
	blockData := it.reader.readBlock(entry.blockOffset, entry.blockSize)
	pairs, err := decodeBlock(blockData)
	if err != nil {
		it.pairs = nil
		return false
	}
	it.pairs = pairs
	it.blockIdx = blockIdx
	it.pairIdx = 0
	return true
}

func (it *sstIterator) Next() bool {
	if it.pairs == nil {
		if !it.loadBlock(0) {
			return false
		}
		return it.pairIdx < len(it.pairs)
	}
	if it.pairIdx+1 < len(it.pairs) {
		it.pairIdx++
		return true
	}
	if !it.loadBlock(it.blockIdx + 1) {
		return false
	}
	return it.pairIdx < len(it.pairs)
}

func (it *sstIterator) Key() []byte {
	if it.pairs == nil || it.pairIdx >= len(it.pairs) {
		return nil
	}
	return it.pairs[it.pairIdx].key
}

func (it *sstIterator) Value() []byte {
	if it.pairs == nil || it.pairIdx >= len(it.pairs) {
		return nil
	}
	return it.pairs[it.pairIdx].value
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
