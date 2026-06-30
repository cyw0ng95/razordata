package ls

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"unsafe"
)

type sstReader struct {
	data            []byte
	indexBlock      []indexEntry
	bloom           []byte
	prefixBloom     []byte
	rangeTombstones []kvPair // REQ001008: sorted [start, end) pairs
	filePath        string   // non-empty for lazy readers (REQ000997)
}

func openSST(data []byte) (*sstReader, error) {
	return openSSTWithPath(data, "")
}

func openSSTWithPath(data []byte, path string) (*sstReader, error) {
	if len(data) < sstFooterSizeOld {
		return nil, ErrInvalidSSTFormat
	}

	r := &sstReader{data: data, filePath: path}

	// REQ001008: detect footer format by checking magic field position.
	// New format (44-byte footer): magic at offset len(data)-44+36 = len(data)-8
	// Old format (28-byte footer): magic at offset len(data)-28+24 = len(data)-4
	var footerStart int
	var footerSize int
	magicNew := len(data) - 8
	magicOld := len(data) - 4

	if magicNew >= 4 && binary.LittleEndian.Uint32(data[magicNew:]) == sstMagic {
		// New format (44-byte footer)
		footerSize = sstFooterSize
		footerStart = len(data) - footerSize
	} else if magicOld >= 4 && binary.LittleEndian.Uint32(data[magicOld:]) == sstMagic {
		// Old format (28-byte footer)
		footerSize = sstFooterSizeOld
		footerStart = len(data) - footerSize
	} else {
		return nil, ErrInvalidSSTFormat
	}

	indexOffset := binary.LittleEndian.Uint64(data[footerStart:])
	indexSize := binary.LittleEndian.Uint32(data[footerStart+8:])
	bloomOffset := binary.LittleEndian.Uint64(data[footerStart+12:])
	bloomSize := binary.LittleEndian.Uint32(data[footerStart+20:])

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
			remaining := dataLen - prefixBloomStart - uint64(footerSize)
			if remaining > 0 && remaining < dataLen && prefixBloomStart+remaining <= dataLen {
				r.prefixBloom = data[prefixBloomStart : prefixBloomStart+remaining]
			}
		}
	}

	// REQ001008: parse range tombstone block if present (new format only)
	if footerSize == sstFooterSize {
		rangeTombstoneOffset := binary.LittleEndian.Uint64(data[footerStart+24:])
		rangeTombstoneSize := binary.LittleEndian.Uint32(data[footerStart+32:])
		if rangeTombstoneOffset > 0 && rangeTombstoneSize > 0 {
			if rangeTombstoneOffset >= dataLen || rangeTombstoneOffset+uint64(rangeTombstoneSize) > dataLen {
				return nil, ErrInvalidSSTFormat
			}
			rtData := data[rangeTombstoneOffset : rangeTombstoneOffset+uint64(rangeTombstoneSize)]
			r.rangeTombstones = parseRangeTombstones(rtData)
		}
	}

	return r, nil
}

// openSSTLazy opens an SST file lazily — only the footer, index block, and
// bloom filter are read upfront. Data blocks are read on demand via readRaw.
// This avoids loading entire SST files into memory during compaction.
func openSSTLazy(path string) (*sstReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	fileSize := stat.Size()
	if fileSize < sstFooterSizeOld {
		return nil, ErrInvalidSSTFormat
	}

	// Read last 44 bytes to detect footer format
	readSize := sstFooterSize
	if fileSize < int64(sstFooterSize) {
		readSize = int(fileSize)
	}
	footerBuf := make([]byte, readSize)
	if _, err := f.ReadAt(footerBuf, fileSize-int64(readSize)); err != nil {
		return nil, err
	}

	// Detect footer format by checking magic field position
	var footerStart int64
	var footerSize int

	// Magic field in new footer (44 bytes) is at offset 36 from footer start,
	// which is readSize-8 from the start of footerBuf.
	// Magic field in old footer (28 bytes) is at offset 24 from footer start,
	// which is readSize-4 from the start of footerBuf.
	offsetNew := readSize - 8
	offsetOld := readSize - 4

	if offsetNew >= 0 && offsetNew+4 <= readSize && binary.LittleEndian.Uint32(footerBuf[offsetNew:]) == sstMagic {
		// New format (44-byte footer)
		footerSize = sstFooterSize
		footerStart = fileSize - int64(footerSize)
	} else if offsetOld >= 0 && offsetOld+4 <= readSize && binary.LittleEndian.Uint32(footerBuf[offsetOld:]) == sstMagic {
		// Old format (28-byte footer)
		footerSize = sstFooterSizeOld
		footerStart = fileSize - int64(footerSize)
	} else {
		return nil, ErrInvalidSSTFormat
	}

	// Read full footer
	footer := make([]byte, footerSize)
	if _, err := f.ReadAt(footer, footerStart); err != nil {
		return nil, err
	}

	indexOffset := binary.LittleEndian.Uint64(footer)
	indexSize := binary.LittleEndian.Uint32(footer[8:])
	bloomOffset := binary.LittleEndian.Uint64(footer[12:])
	bloomSize := binary.LittleEndian.Uint32(footer[20:])

	r := &sstReader{filePath: path}

	// Read index block
	if indexOffset > 0 {
		if indexOffset >= uint64(fileSize) || indexOffset+uint64(indexSize) > uint64(fileSize) {
			// REQ001008: range tombstones should suppress keys in the range
			return nil, ErrInvalidSSTFormat
		}
		indexData := make([]byte, indexSize)
		if _, err := f.ReadAt(indexData, int64(indexOffset)); err != nil {
			return nil, err
		}
		r.indexBlock = parseIndexBlock(indexData)
	}

	// Read bloom filter
	if bloomOffset > 0 {
		if bloomOffset >= uint64(fileSize) || bloomOffset+uint64(bloomSize) > uint64(fileSize) {
			return nil, ErrInvalidSSTFormat
		}
		if indexOffset > 0 && indexOffset+uint64(indexSize) > bloomOffset {
			return nil, ErrInvalidSSTFormat
		}
		bloomData := make([]byte, bloomSize)
		if _, err := f.ReadAt(bloomData, int64(bloomOffset)); err != nil {
			return nil, err
		}
		r.bloom = bloomData

		// Read prefix bloom
		prefixBloomStart := bloomOffset + uint64(bloomSize)
		if prefixBloomStart < uint64(fileSize) {
			remaining := uint64(fileSize) - prefixBloomStart - uint64(footerSize)
			if remaining > 0 && remaining < uint64(fileSize) && prefixBloomStart+remaining <= uint64(fileSize) {
				prefixBloomData := make([]byte, remaining)
				if _, err := f.ReadAt(prefixBloomData, int64(prefixBloomStart)); err != nil {
					return nil, err
				}
				r.prefixBloom = prefixBloomData
			}
		}
	}

	// REQ001008: read range tombstone block if present (new format only)
	if footerSize == sstFooterSize {
		rangeTombstoneOffset := binary.LittleEndian.Uint64(footer[24:])
		rangeTombstoneSize := binary.LittleEndian.Uint32(footer[32:])
		if rangeTombstoneOffset > 0 && rangeTombstoneSize > 0 {
			if rangeTombstoneOffset >= uint64(fileSize) || rangeTombstoneOffset+uint64(rangeTombstoneSize) > uint64(fileSize) {
				return nil, ErrInvalidSSTFormat
			}
			rtData := make([]byte, rangeTombstoneSize)
			if _, err := f.ReadAt(rtData, int64(rangeTombstoneOffset)); err != nil {
				return nil, err
			}
			r.rangeTombstones = parseRangeTombstones(rtData)
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
			// REQ001008: range tombstones should suppress keys in the range
			blockSize: int(blockSize),
		})
	}

	return entries
}

// parseRangeTombstones parses the range tombstone block.
// Format: [start_len][start][end_len][end]...
// REQ001008.
func parseRangeTombstones(data []byte) []kvPair {
	var pairs []kvPair
	offset := 0

	for offset < len(data) {
		if offset+1 > len(data) {
			break
		}
		startLen, n := decodeVarint(data[offset:])
		offset += n

		if offset+int(startLen) > len(data) {
			break
		}
		start := data[offset : offset+int(startLen)]
		offset += int(startLen)

		if offset+1 > len(data) {
			break
		}
		endLen, n := decodeVarint(data[offset:])
		offset += n

		if offset+int(endLen) > len(data) {
			break
		}
		end := data[offset : offset+int(endLen)]
		offset += int(endLen)

		pairs = append(pairs, kvPair{key: start, value: end})
	}

	return pairs
}

func (r *sstReader) mayContain(key []byte) bool {
	if len(r.bloom) == 0 {
		return true
	}

	h1 := fnv1aHash(key, fnv1aOffset32)
	h2 := fnv1aHash(key, fnv1aPrime32)

	mask := uint32(len(r.bloom)*8) - 1
	bucket1 := int(h1 & mask)
	// REQ001008: range tombstones should suppress keys in the range
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
	// REQ001008: check range tombstones before scanning data blocks.
	// If the key falls within any range tombstone, return nil, false.
	if r.isInRangeTombstone(key) {
		return nil, false
	}

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

// isInRangeTombstone checks if key falls within any range tombstone [start, end).
// REQ001008: range tombstones suppress all keys in the range.
func (r *sstReader) isInRangeTombstone(key []byte) bool {
	for _, rt := range r.rangeTombstones {
		// rt.key = start, rt.value = end
		if bytes.Compare(key, rt.key) >= 0 && bytes.Compare(key, rt.value) < 0 {
			return true
		}
	}
	return false
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
	raw := r.readRaw(offset, size)
	if raw == nil {
		return nil
	}
	decompressed, err := decompressBlockDict(raw)
	if err != nil {
		if d2, err2 := decompressBlock(raw); err2 == nil {
			return d2
		}
		return raw // fallback to raw data
	}
	return decompressed
}

func (r *sstReader) readRaw(offset, size int) []byte {
	if offset < 0 || offset >= len(r.data) {
		if r.filePath == "" {
			return nil
		}
		return r.readFromFile(offset, size)
	}
	end := offset + size
	if end > len(r.data) {
		end = len(r.data)
	}
	return r.data[offset:end]
}

func (r *sstReader) readFromFile(offset, size int) []byte {
	f, err := os.Open(r.filePath)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, size)
	n, err := f.ReadAt(buf, int64(offset))
	if err != nil || n < size {
		return nil
	}
	return buf
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
