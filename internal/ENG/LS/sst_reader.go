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

	// REQ000603: bounds-check every footer-derived slice before
	// indexing into data. A truncated or corrupt SST with an
	// out-of-range offset/size would otherwise panic with
	// index-out-of-range. Also enforce the layout invariant
	// that the index block precedes the bloom block.
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

	// FNV-1a double hashing per ENG.md spec (REQ000174)
	h1 := fnv1aHash(key, fnv1aOffset32)
	h2 := fnv1aHash(key, fnv1aPrime32)

	// REQ000615: modulo before cast to avoid negative indices on
	// 32-bit platforms where int is 32 bits.
	size := uint32(len(r.bloom) * 8)
	bucket1 := int(h1 % size)
	bucket2 := int(h2 % size)

	return (r.bloom[bucket1/8]&(1<<(bucket1%8)) != 0) &&
		(r.bloom[bucket2/8]&(1<<(bucket2%8)) != 0)
}

// MayContainPrefix checks if the SST might contain a key with the given prefix.
// REQ000047 — prefix bloom filters for range scans.
// REQ000599: the modulus is the byte count, NOT the bit count.
// The writer (setPrefixBloomBit) does \`int(h1) % size\` where size
// is the bloom's byte length, so bits are set at indices [0, size*8).
// A reader using bit count (\`len(r.prefixBloom) * 8\`) would compute
// the same bit indices, but the writer caps at byte-count buckets
// effectively reducing the bit address space to size*8 - (size*8 % 8)
// = size*8 with collisions on every 8th bit. More importantly the
// mismatch caused 7/8 of prefix queries to produce false negatives
// (filter reports "not present" for prefixes that exist).
func (r *sstReader) MayContainPrefix(prefix []byte) bool {
	if len(r.prefixBloom) == 0 {
		return true
	}
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	// Match the writer's modulus: byte count, not bit count.
	size := uint32(len(r.prefixBloom))
	h1 := fnv1aHash(prefix, fnv1aOffset32)
	h2 := fnv1aHash(prefix, fnv1aPrime32)
	// REQ000615: modulo before cast.
	bucket1 := int(h1 % size)
	bucket2 := int(h2 % size)
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
	// REQ000271 + REQ000297: decompress block. The flag byte
	// indicates plain (0/1) or dictionary-compressed (2) — see
	// compressBlockDict / decompressBlockDict.
	decompressed, err := decompressBlockDict(raw)
	if err != nil {
		// Backward compat: try the old plain decompress.
		if d2, err2 := decompressBlock(raw); err2 == nil {
			return d2
		}
		return raw // fallback to raw data
	}
	return decompressed
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
	for i := 0; i < restartCount; i++ {
		if restartOffset+i*4+4 <= len(data)-4 {
			restartPoints[i] = int(binary.LittleEndian.Uint32(data[restartOffset+i*4:]))
		}
	}

	// R188-2: when restartCount=0, the block has no restart
	// points; treat the whole blockData as a single segment
	// so the entry-decode loop runs at least once.
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
			// Entry format (R188-3): [keyLen:varint]
			// [key:keyLen bytes] [valueLen:varint]
			// [value:valueLen bytes]. The decoder must
			// read the key BYTES between the two
			// varints — earlier versions of the decoder
			// read valueLen immediately after keyLen,
			// which corrupted every entry.
			keyLen, n := decodeVarint(blockData[pos:])
			pos += n

			if pos+int(keyLen) > len(blockData) {
				break
			}
			key := blockData[pos : pos+int(keyLen)]
			pos += int(keyLen)

			valueLen, n := decodeVarint(blockData[pos:])
			pos += n

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

// loadBlock reads reader.indexBlock[blockIdx] and decodes its
// kvPairs. Returns false if blockIdx is out of range.
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
	// First call: pairs is nil, blockIdx is -1. Load block 0.
	if it.pairs == nil {
		if !it.loadBlock(0) {
			return false
		}
		return it.pairIdx < len(it.pairs)
	}
	// Advance within the current block.
	if it.pairIdx+1 < len(it.pairs) {
		it.pairIdx++
		return true
	}
	// Current block exhausted; advance to next block.
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
