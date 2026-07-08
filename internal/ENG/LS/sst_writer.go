package ls

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"sync"
)

var crc32Koopman = crc32.MakeTable(crc32.Koopman) // REQ000588: pre-allocated

const (
	sstBlockSize             = 4 * 1024
	sstFooterSize            = 44 // REQ001008: extended from 28 to 44 (added range tombstone offset/size + version)
	sstFooterSizeOld         = 28 // legacy footer size for backward compatibility
	sstMagic                 = 0x52545453
	sstVersionRangeTombstone = 1 // REQ001008: SST version with range tombstone support
)

var (
	ErrInvalidSSTFormat = errors.New("ls: invalid SST format")
	ErrBlockNotFound    = errors.New("ls: block not found")
)

type indexEntry struct {
	largestKey  []byte
	blockOffset int
	blockSize   int
}

type sstWriter struct {
	blocks       [][]byte
	indexEntries []indexEntry
	bloom        []byte
	prefixBloom  []byte
	ribbon       []byte // REQ001169: Ribbon filter (v2 SSTs)
	keys         [][]byte
	// REQ001008: range tombstones stored as [start, end) pairs.
	// Sorted by start key. Written to a separate block in the SST.
	rangeTombstones    [][]byte // interleaved: start1, end1, start2, end2, ...
	keyCount           int
	minKey             []byte
	maxKey             []byte
	lastKey            []byte
	currentBlockOffset int
	version            int // REQ001169: SST format version
	bitsPerKey         int // REQ001401: per-level bits/key for Ribbon filter
}

var sstWriterPool = sync.Pool{
	New: func() any {
		return &sstWriter{
			blocks:          make([][]byte, 0, 16),
			indexEntries:    make([]indexEntry, 0, 16),
			keys:            make([][]byte, 0, 256),
			rangeTombstones: make([][]byte, 0, 16),
		}
	},
}

func acquireSSTWriter() *sstWriter {
	return sstWriterPool.Get().(*sstWriter)
}

func releaseSSTWriter(w *sstWriter) {
	w.Reset()
	sstWriterPool.Put(w)
}

func newSSTWriter() *sstWriter {
	return &sstWriter{
		blocks:          make([][]byte, 0, 16),
		indexEntries:    make([]indexEntry, 0, 16),
		keys:            make([][]byte, 0, 256),
		rangeTombstones: make([][]byte, 0, 16),
	}
}

func bloomSizeFor(n int) int {
	if n <= 0 {
		return 1
	}
	return (n*10 + 7) / 8
}

// bloomSizeForPow2 returns a byte count that is a power of 2,
// large enough to hold bloomSizeFor(n) bytes.
func bloomSizeForPow2(n int) int {
	// REQ001008: range tombstones should suppress keys in the range
	base := bloomSizeFor(n)
	return int(nextPow2(uint32(base)))
}

// SetLevel configures the SST writer for a specific LSM level.
// This determines the bits-per-key ratio used for the Ribbon filter.
func (w *sstWriter) SetLevel(level int) {
	w.bitsPerKey = bitsPerKeyForLevel(level)
}

func (w *sstWriter) Add(key, value []byte) {
	if w.keyCount == 0 || bytes.Compare(key, w.minKey) < 0 {
		w.minKey = append(w.minKey[:0], key...)
	}
	if w.keyCount == 0 || bytes.Compare(key, w.maxKey) > 0 {
		w.maxKey = append(w.maxKey[:0], key...)
	}

	var buf bytes.Buffer
	buf.Write(encodeVarint(int64(len(key))))
	buf.Write(key)
	buf.Write(encodeVarint(int64(len(value))))
	buf.Write(value)

	if len(w.blocks) == 0 {
		w.blocks = append(w.blocks, make([]byte, 0, sstBlockSize))
	}

	currentBlock := w.blocks[len(w.blocks)-1]
	if len(currentBlock) == 0 {
		w.blocks[len(w.blocks)-1] = append(currentBlock, buf.Bytes()...)
		w.keyCount++
		w.keys = append(w.keys, append([]byte(nil), key...))
		w.lastKey = append(w.lastKey[:0], key...)
		return
	}
	if len(currentBlock)+buf.Len() > sstBlockSize {
		w.finishCurrentBlock()
		w.blocks = append(w.blocks, make([]byte, 0, sstBlockSize))
	}

	w.blocks[len(w.blocks)-1] = append(w.blocks[len(w.blocks)-1], buf.Bytes()...)
	w.keyCount++
	w.keys = append(w.keys, append([]byte(nil), key...))
	w.lastKey = append(w.lastKey[:0], key...)
}

// AddRangeTombstone adds a range tombstone covering [start, end).
// REQ001008: range tombstones suppress all keys in the range.
func (w *sstWriter) AddRangeTombstone(start, end []byte) {
	w.rangeTombstones = append(w.rangeTombstones, append([]byte(nil), start...))
	w.rangeTombstones = append(w.rangeTombstones, append([]byte(nil), end...))
}

func (w *sstWriter) setBloomBitForSize(key []byte, size int) {
	h1 := fnv1aHash(key, fnv1aOffset32)
	h2 := fnv1aHash(key, fnv1aPrime32)

	bitCount := uint32(size * 8)
	mask := bitCount - 1
	bucket1 := int(h1 & mask)
	bucket2 := int(h2 & mask)

	w.bloom[bucket1/8] |= 1 << (bucket1 % 8)
	w.bloom[bucket2/8] |= 1 << (bucket2 % 8)
}

func (w *sstWriter) setPrefixBloomBit(prefix []byte, size int) {
	h1 := fnv1aHash(prefix, fnv1aOffset32)
	h2 := fnv1aHash(prefix, fnv1aPrime32)
	mask := uint32(size) - 1
	bucket1 := int(h1 & mask)
	bucket2 := int(h2 & mask)
	if bucket1/8 < len(w.prefixBloom) {
		w.prefixBloom[bucket1/8] |= 1 << (bucket1 % 8)
	}
	if bucket2/8 < len(w.prefixBloom) {
		w.prefixBloom[bucket2/8] |= 1 << (bucket2 % 8)
	}
}

func (w *sstWriter) finishCurrentBlock() {
	if len(w.blocks) == 0 {
		return
	}

	block := w.blocks[len(w.blocks)-1]
	if len(block) == 0 {
		return
	}

	checksum := crc32.Checksum(block, crc32Koopman)
	block = append(block, 0, 0, 0, 0)
	block = append(block, byte(checksum), byte(checksum>>8), byte(checksum>>16), byte(checksum>>24))

	blockLen := len(block)
	w.blocks[len(w.blocks)-1] = block

	w.indexEntries = append(w.indexEntries, indexEntry{
		largestKey:  append([]byte(nil), w.lastKey...),
		blockOffset: w.currentBlockOffset,
		blockSize:   blockLen,
	})
	w.currentBlockOffset += blockLen
}

func (w *sstWriter) Finish() ([]byte, error) {
	if len(w.blocks) == 0 && len(w.rangeTombstones) == 0 {
		return nil, nil
	}

	w.finishCurrentBlock()

// REQ001169: default to Ribbon filter (v2) for new SSTs.
	// Old Bloom (v1) is kept for backward compat via explicit version.
	if w.version == 0 {
		w.version = sstVersionRibbon
	}

	// Use level-based bits-per-key if set, otherwise default to 10.
	bpk := w.bitsPerKey
	if bpk <= 0 {
		bpk = 10
	}

	if w.version >= sstVersionRibbon {
		// Build Ribbon filter instead of double-bloom.
		ribbonSize := ribbonSizeForPow2(w.keyCount, bpk)
		w.ribbon = buildRibbonFilterWithSize(w.keys, ribbonSize*8)
		// No prefix bloom needed; Ribbon filter covers all queries.
		w.prefixBloom = nil
	} else {
		// Legacy Bloom filter path (v1).
		bloomSize := bloomSizeForPow2(w.keyCount)
		w.bloom = make([]byte, bloomSize)

		prefixBloomSize := bloomSizeForPow2(w.keyCount)
		w.prefixBloom = make([]byte, prefixBloomSize)

		for _, k := range w.keys {
			w.setBloomBitForSize(k, bloomSize)
			prefix := k
			if len(prefix) > 8 {
				prefix = prefix[:8]
			}
			w.setPrefixBloomBit(prefix, prefixBloomSize)
		}
		w.ribbon = nil
	}

	var buf bytes.Buffer

	// Write compressed data blocks
	sharedDict := trainSSTDict(w.blocks, 4096)
	compressedOffsets := make([]int, len(w.blocks))
	for i, block := range w.blocks {
		compressedOffsets[i] = buf.Len()
		compressed, err := compressBlockDictShared(block, sharedDict)
		if err != nil {
			return nil, err
		}
		buf.Write(compressed)
	}
	for i := range w.indexEntries {
		start := compressedOffsets[i]
		var end int
		if i+1 < len(compressedOffsets) {
			end = compressedOffsets[i+1]
		} else {
			end = buf.Len()
		}
		w.indexEntries[i].blockOffset = start
		w.indexEntries[i].blockSize = end - start
	}

	// REQ001008: write range tombstone block after data blocks, before index
	rangeTombstoneOffset := 0
	rangeTombstoneSize := 0
	if len(w.rangeTombstones) > 0 {
		rangeTombstoneOffset = buf.Len()
		for i := 0; i < len(w.rangeTombstones); i += 2 {
			start := w.rangeTombstones[i]
			end := w.rangeTombstones[i+1]
			buf.Write(encodeVarint(int64(len(start))))
			buf.Write(start)
			buf.Write(encodeVarint(int64(len(end))))
			buf.Write(end)
		}
		rangeTombstoneSize = buf.Len() - rangeTombstoneOffset
	}

	// Write index block
	indexOffset := buf.Len()
	offsetBuf := make([]byte, 8)
	sizeBuf := make([]byte, 8)
	for _, entry := range w.indexEntries {
		keyLen := encodeVarint(int64(len(entry.largestKey)))
		buf.Write(keyLen)
		buf.Write(entry.largestKey)
		binary.LittleEndian.PutUint64(offsetBuf, uint64(entry.blockOffset))
		buf.Write(offsetBuf)
		binary.LittleEndian.PutUint64(sizeBuf, uint64(entry.blockSize))
		buf.Write(sizeBuf)
	}
	indexSize := buf.Len() - indexOffset

	// Write bloom filters (or Ribbon filter for v2).
	bloomOffset := buf.Len()
	if w.version >= sstVersionRibbon {
		buf.Write(w.ribbon)
		// No prefix bloom for v2.
	} else {
		buf.Write(w.bloom)
		buf.Write(w.prefixBloom)
	}

	// REQ001008: write footer with range tombstone metadata.
	footer := make([]byte, sstFooterSize)
	// [0:8] index_offset
	binary.LittleEndian.PutUint64(footer[0:8], uint64(indexOffset))
	// [8:12] index_size
	binary.LittleEndian.PutUint32(footer[8:12], uint32(indexSize))
	// [12:20] bloom_offset
	binary.LittleEndian.PutUint64(footer[12:20], uint64(bloomOffset))
	// [20:24] bloom_size — for v2 this is the ribbon filter size.
	if w.version >= sstVersionRibbon {
		binary.LittleEndian.PutUint32(footer[20:24], uint32(len(w.ribbon)))
	} else {
		binary.LittleEndian.PutUint32(footer[20:24], uint32(bloomSizeForPow2(w.keyCount)))
	}
	// [24:32] range_tombstone_offset
	binary.LittleEndian.PutUint64(footer[24:32], uint64(rangeTombstoneOffset))
	// [32:36] range_tombstone_size
	binary.LittleEndian.PutUint32(footer[32:36], uint32(rangeTombstoneSize))
	// [36:40] magic
	binary.LittleEndian.PutUint32(footer[36:40], sstMagic)
	// [40:44] version
	binary.LittleEndian.PutUint32(footer[40:44], uint32(w.version))
	buf.Write(footer)

	return buf.Bytes(), nil
}

func (w *sstWriter) Reset() {
	w.blocks = w.blocks[:0]
	w.indexEntries = w.indexEntries[:0]
	w.keys = w.keys[:0]
	w.bloom = nil
	w.prefixBloom = nil
	w.ribbon = nil
	w.keyCount = 0
	w.minKey = w.minKey[:0]
	w.maxKey = w.maxKey[:0]
	w.rangeTombstones = w.rangeTombstones[:0]
	w.version = 0
	w.bitsPerKey = 0
}

// compressBlock compresses a data block using flate (REQ000271).
func compressBlock(block []byte) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(1) // compressed flag
	w, err := flate.NewWriter(&buf, flate.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(block); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	compressed := buf.Bytes()
	if len(compressed) < len(block)+1 {
		return compressed, nil
	}
	out := make([]byte, 0, len(block)+1)
	out = append(out, 0) // uncompressed flag
	out = append(out, block...)
	return out, nil
}

func decompressBlock(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty block")
	}
	flag := data[0]
	payload := data[1:]
	if flag == 0 {
		return payload, nil
	}
	r := flate.NewReader(bytes.NewReader(payload))
	defer r.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
