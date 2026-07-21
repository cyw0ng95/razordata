package ls

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"strconv"
	"unsafe"

	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
)

type sstReader struct {
	fs               FS // REQ001172: virtual filesystem
	data             []byte
	indexBlock       []indexEntry
	bloom            []byte
	prefixBloom      []byte
	ribbon           []byte   // REQ001169: Ribbon filter (v2+ SSTs)
	rangeTombstones  []kvPair // REQ001008: sorted [start, end) pairs
	filePath         string   // non-empty for lazy readers (REQ000997)
	version          int      // REQ001169: SST format version from footer
	useInterpolation bool     // REQ001170: enable interpolation search for uniform keys

	// REQ001227: mmap-backed zero-copy reads
	mmap     []byte // memory-mapped file region (read-only)
	mmapSize int64

	// REQ001242: shared block cache for decompressed SST blocks
	blockCache *BlockCache

	lazyFD File // cached fd for lazy readers, nil if using mmap/data
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

	// REQ001169: read SST format version from footer
	if footerSize == sstFooterSize {
		r.version = int(binary.LittleEndian.Uint32(data[footerStart+40:]))
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
		// REQ001170: detect key uniformity for interpolation search.
		r.useInterpolation = detectKeyUniformity(r.indexBlock)
	}

	if bloomOffset > 0 {
		if bloomOffset >= dataLen || bloomOffset+uint64(bloomSize) > dataLen {
			return nil, ErrInvalidSSTFormat
		}
		if indexOffset > 0 && indexOffset+uint64(indexSize) > bloomOffset {
			// index must come before bloom in the file
			return nil, ErrInvalidSSTFormat
		}
		// REQ001169: for Ribbon (v2+) SSTs, use ribbon filter instead of bloom.
		if r.version >= sstVersionRibbon {
			r.ribbon = data[bloomOffset : bloomOffset+uint64(bloomSize)]
		} else {
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
	return openSSTLazyWithFS(DefaultFS(), path)
}

// openSSTLazyWithFS is like openSSTLazy but uses the provided FS.
func openSSTLazyWithFS(fs FS, path string) (*sstReader, error) {
	f, err := fs.Open(path)
	if err != nil {
		return nil, err
	}
	// f is closed on error paths below; on success it is stored
	// in the reader and closed via sstReader.Close().

	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	fileSize := stat.Size()
	if fileSize < sstFooterSizeOld {
		f.Close()
		return nil, ErrInvalidSSTFormat
	}

	// Read last 44 bytes to detect footer format
	readSize := sstFooterSize
	if fileSize < int64(sstFooterSize) {
		readSize = int(fileSize)
	}
	footerBuf := make([]byte, readSize)
	if _, err := f.ReadAt(footerBuf, fileSize-int64(readSize)); err != nil {
		f.Close()
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
		f.Close()
		return nil, ErrInvalidSSTFormat
	}

	// Read full footer
	footer := make([]byte, footerSize)
	if _, err := f.ReadAt(footer, footerStart); err != nil {
		f.Close()
		return nil, err
	}

	indexOffset := binary.LittleEndian.Uint64(footer)
	indexSize := binary.LittleEndian.Uint32(footer[8:])
	bloomOffset := binary.LittleEndian.Uint64(footer[12:])
	bloomSize := binary.LittleEndian.Uint32(footer[20:])

	r := &sstReader{fs: fs, filePath: path, lazyFD: f}

	// REQ001169: read SST format version from footer
	if footerSize == sstFooterSize {
		r.version = int(binary.LittleEndian.Uint32(footer[40:]))
	}

	// Read index block
	if indexOffset > 0 {
		if indexOffset >= uint64(fileSize) || indexOffset+uint64(indexSize) > uint64(fileSize) {
			f.Close()
			return nil, ErrInvalidSSTFormat
		}
		indexData := make([]byte, indexSize)
		if _, err := f.ReadAt(indexData, int64(indexOffset)); err != nil {
			f.Close()
			return nil, err
		}
		r.indexBlock = parseIndexBlock(indexData)
		r.useInterpolation = detectKeyUniformity(r.indexBlock)
	}

	// Read bloom filter (or Ribbon filter for v2+).
	if bloomOffset > 0 {
		if bloomOffset >= uint64(fileSize) || bloomOffset+uint64(bloomSize) > uint64(fileSize) {
			f.Close()
			return nil, ErrInvalidSSTFormat
		}
		if indexOffset > 0 && indexOffset+uint64(indexSize) > bloomOffset {
			f.Close()
			return nil, ErrInvalidSSTFormat
		}
		bloomData := make([]byte, bloomSize)
		if _, err := f.ReadAt(bloomData, int64(bloomOffset)); err != nil {
			f.Close()
			return nil, err
		}
		if r.version >= sstVersionRibbon {
			r.ribbon = bloomData
		} else {
			r.bloom = bloomData

			prefixBloomStart := bloomOffset + uint64(bloomSize)
			if prefixBloomStart < uint64(fileSize) {
				remaining := uint64(fileSize) - prefixBloomStart - uint64(footerSize)
				if remaining > 0 && remaining < uint64(fileSize) && prefixBloomStart+remaining <= uint64(fileSize) {
					prefixBloomData := make([]byte, remaining)
					if _, err := f.ReadAt(prefixBloomData, int64(prefixBloomStart)); err != nil {
						f.Close()
						return nil, err
					}
					r.prefixBloom = prefixBloomData
				}
			}
		}
	}

	// REQ001008: read range tombstone block if present (new format only)
	if footerSize == sstFooterSize {
		rangeTombstoneOffset := binary.LittleEndian.Uint64(footer[24:])
		rangeTombstoneSize := binary.LittleEndian.Uint32(footer[32:])
		if rangeTombstoneOffset > 0 && rangeTombstoneSize > 0 {
			if rangeTombstoneOffset >= uint64(fileSize) || rangeTombstoneOffset+uint64(rangeTombstoneSize) > uint64(fileSize) {
				f.Close()
				return nil, ErrInvalidSSTFormat
			}
			rtData := make([]byte, rangeTombstoneSize)
			if _, err := f.ReadAt(rtData, int64(rangeTombstoneOffset)); err != nil {
				f.Close()
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
		if len(entries) > 1 {
			prev := entries[len(entries)-2].largestKey
			EC.BUG_ON(bytes.Compare(prev, key) >= 0, "sst.parseIndexBlock: SST index key ordering violation, prev=%q >= curr=%q", prev, key)
		}
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
	// REQ001169: use Ribbon filter for v2+ SSTs.
	if len(r.ribbon) > 0 {
		return mayContainRibbon(r.ribbon, key, ribbonWidth)
	}

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
// REQ001169: for v2+ SSTs, uses Ribbon filter instead of prefix bloom.
func (r *sstReader) MayContainPrefix(prefix []byte) bool {
	// REQ001169: Ribbon filter covers both exact-key and prefix queries.
	if len(r.ribbon) > 0 {
		if len(prefix) > 8 {
			prefix = prefix[:8]
		}
		return mayContainRibbon(r.ribbon, prefix, ribbonWidth)
	}

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

	blockData := r.readBlock(r.indexBlock[blockIdx].blockOffset, r.indexBlock[blockIdx].blockSize, blockIdx)
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

	// REQ001170: use interpolation search when keys are uniformly distributed.
	if r.useInterpolation && len(r.indexBlock) > 4 {
		return r.searchIndexInterpolation(key)
	}

	return r.searchIndexBinary(key)
}

// searchIndexBinary performs standard binary search on the index block.
func (r *sstReader) searchIndexBinary(key []byte) int {
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

	return r.clampIndex(lo)
}

// searchIndexInterpolation performs interpolation search on the index block.
// For uniformly distributed keys, interpolation search probes closer to the
// expected position, reducing the number of comparisons.
//
// REQ001170: falls back to binary search if the probe is outside the current
// search bounds (non-uniform segment within a uniform data set).
func (r *sstReader) searchIndexInterpolation(key []byte) int {
	n := len(r.indexBlock)
	lo, hi := 0, n-1

	firstNumeric := keyToNumeric(r.indexBlock[0].largestKey)
	lastNumeric := keyToNumeric(r.indexBlock[hi].largestKey)

	for lo <= hi {
		// If search range is small, use binary search.
		if hi-lo < 8 {
			for i := lo; i <= hi; i++ {
				if bytes.Compare(r.indexBlock[i].largestKey, key) >= 0 {
					return r.clampIndex(i)
				}
			}
			return r.clampIndex(hi + 1)
		}

		rangeVal := lastNumeric - firstNumeric
		if rangeVal == 0 {
			mid := (lo + hi) / 2
			cmp := bytes.Compare(r.indexBlock[mid].largestKey, key)
			if cmp >= 0 {
				hi = mid - 1
			} else {
				lo = mid + 1
			}
			continue
		}

		probeNumeric := keyToNumeric(key)

		// Compute interpolation probe position.
		probe := lo + int(float64(hi-lo)*float64(probeNumeric-firstNumeric)/float64(rangeVal))

		// Clamp to current search range.
		if probe < lo {
			probe = lo
		}
		if probe > hi {
			probe = hi
		}

		cmp := bytes.Compare(r.indexBlock[probe].largestKey, key)
		if cmp >= 0 {
			hi = probe - 1
			lastNumeric = keyToNumeric(r.indexBlock[min(hi, n-1)].largestKey)
		} else {
			lo = probe + 1
			firstNumeric = keyToNumeric(r.indexBlock[lo].largestKey)
		}
	}

	return r.clampIndex(lo)
}

// clampIndex clamps the index to a valid range.
func (r *sstReader) clampIndex(lo int) int {
	if lo >= len(r.indexBlock) {
		return len(r.indexBlock) - 1
	}
	if lo < 0 {
		return 0
	}
	return lo
}

// keyToNumeric converts the first 8 bytes of a key to a uint64 for
// interpolation estimation. Keys shorter than 8 bytes are right-padded
// with zeros.
func keyToNumeric(key []byte) uint64 {
	if len(key) >= 8 {
		return binary.LittleEndian.Uint64(key[:8])
	}
	var buf [8]byte
	copy(buf[:], key)
	return binary.LittleEndian.Uint64(buf[:])
}

// detectKeyUniformity checks if the index block keys are uniformly distributed
// by computing the coefficient of variation (CV) of key lengths.
// A CV below 0.3 indicates uniform key sizes and enables interpolation search.
//
// REQ001170.
func detectKeyUniformity(indexBlock []indexEntry) bool {
	if len(indexBlock) < 4 {
		return false
	}

	var sumLen, sumSq float64
	n := float64(len(indexBlock))
	for _, e := range indexBlock {
		l := float64(len(e.largestKey))
		sumLen += l
		sumSq += l * l
	}

	mean := sumLen / n
	if mean < 1 {
		return false
	}

	variance := sumSq/n - mean*mean
	if variance < 0 {
		variance = 0
	}

	sd := sqrtFloat64(variance)
	cv := sd / mean

	return cv > 0 && cv < 0.3
}

// sqrtFloat64 computes sqrt(x) using Newton's method.
func sqrtFloat64(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for range 15 {
		z = (z + x/z) * 0.5
	}
	return z
}

func (r *sstReader) readBlock(offset, size int, blockIdx int) []byte {
	// REQ001242: check block cache before decompressing
	if r.blockCache != nil && r.filePath != "" {
		key := r.filePath + ":" + strconv.Itoa(blockIdx)
		if data, ok := r.blockCache.Get(key); ok {
			return data
		}
	}
	raw := r.readRaw(offset, size)
	if raw == nil {
		return nil
	}
	decompressed, err := decompressBlockDict(raw)
	if err != nil {
		if d2, err2 := decompressBlock(raw); err2 == nil {
			decompressed = d2
		} else {
			decompressed = raw // fallback to raw data
		}
	}
	// REQ001242: store in cache
	if r.blockCache != nil && r.filePath != "" {
		key := r.filePath + ":" + strconv.Itoa(blockIdx)
		r.blockCache.Put(key, decompressed)
	}
	return decompressed
}

func (r *sstReader) readRaw(offset, size int) []byte {
	// REQ001227: prefer mmap for zero-copy reads
	if len(r.mmap) > 0 {
		if offset < 0 || offset >= len(r.mmap) {
			return nil
		}
		end := offset + size
		if end > len(r.mmap) {
			end = len(r.mmap)
		}
		return r.mmap[offset:end]
	}
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
	if r.lazyFD == nil {
		return nil
	}
	buf := make([]byte, size)
	n, err := r.lazyFD.ReadAt(buf, int64(offset))
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
	blockData := it.reader.readBlock(entry.blockOffset, entry.blockSize, blockIdx)
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
	for {
		if it.pairs == nil {
			if !it.loadBlock(0) {
				return false
			}
			it.pairIdx = 0
		} else if it.pairIdx+1 < len(it.pairs) {
			it.pairIdx++
		} else if !it.loadBlock(it.blockIdx + 1) {
			return false
		} else {
			it.pairIdx = 0
		}

		if it.pairs == nil || it.pairIdx >= len(it.pairs) {
			continue
		}

		kv := it.pairs[it.pairIdx]
		if isTombstone(kv.value) {
			it.pairIdx++
			continue
		}
		if len(kv.value) == 0 {
			it.pairIdx++
			continue
		}
		if it.reader.isInRangeTombstone(kv.key) {
			it.pairIdx++
			continue
		}

		return true
	}
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
	if len(r.mmap) > 0 {
		return munmapFile(r.mmap)
	}
	if r.lazyFD != nil {
		err := r.lazyFD.Close()
		r.lazyFD = nil
		return err
	}
	return nil
}


// ParseBlockStatsBlob decodes a block stats blob produced by
// sstWriter.BlockStatsBlob(). Returns per-block stats as a slice
// of byte slices. REQ001656.
func ParseBlockStatsBlob(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	numBlocks, n := decodeVarint(data)
	if n <= 0 || numBlocks <= 0 {
		return nil
	}
	pos := n
	stats := make([][]byte, numBlocks)
	for i := int64(0); i < numBlocks; i++ {
		if pos >= len(data) {
			break
		}
		blobLen, m := decodeVarint(data[pos:])
		if m <= 0 {
			break
		}
		pos += m
		if blobLen > 0 {
			if pos+int(blobLen) > len(data) {
				break
			}
			stats[i] = data[pos : pos+int(blobLen)]
			pos += int(blobLen)
		}
	}
	return stats
}
