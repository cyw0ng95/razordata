package ls

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

const (
	sstBlockSize  = 4 * 1024
	sstFooterSize = 28
	sstMagic      = 0x52545453
)

var (
	ErrInvalidSSTFormat = errors.New("invalid SST format")
	ErrBlockNotFound    = errors.New("block not found")
)

type indexEntry struct {
	largestKey  []byte
	blockOffset int
	blockSize   int
}

type sstWriter struct {
	blocks             [][]byte
	indexEntries       []indexEntry
	bloom              []byte
	prefixBloom        []byte
	keys               [][]byte
	keyCount           int
	minKey             []byte
	maxKey             []byte
	lastKey            []byte
	currentBlockOffset int
}

func newSSTWriter() *sstWriter {
	return &sstWriter{
		blocks:       make([][]byte, 0, 16),
		indexEntries: make([]indexEntry, 0),
		keys:         make([][]byte, 0, 256),
	}
}

// bloomSizeFor returns the bloom filter byte size for n keys
// at10 bits/key (ENG.md:91-92). The ceiling division
// guarantees at least1 byte even for empty inputs so the
// footer always encodes a positive size.
func bloomSizeFor(n int) int {
	if n <= 0 {
		return 1
	}
	return (n*10 + 7) / 8
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
	if len(currentBlock)+buf.Len() > sstBlockSize {
		w.finishCurrentBlock()
		w.blocks = append(w.blocks, make([]byte, 0, sstBlockSize))
	}

	w.blocks[len(w.blocks)-1] = append(w.blocks[len(w.blocks)-1], buf.Bytes()...)
	w.keyCount++
	w.keys = append(w.keys, append([]byte(nil), key...))
	w.lastKey = append(w.lastKey[:0], key...)
}

// setBloomBitForSize sets the two bloom bits for key against a
// bloom filter of byte size size. Uses FNV-1a double hashing
// per ENG.md spec (REQ000174).
func (w *sstWriter) setBloomBitForSize(key []byte, size int) {
	h1 := fnv1aHash(key, fnv1aOffset32)
	h2 := fnv1aHash(key, fnv1aPrime32)

	bitCount := size * 8
	bucket1 := int(h1) % bitCount
	bucket2 := int(h2) % bitCount

	w.bloom[bucket1/8] |= 1 << (bucket1 % 8)
	w.bloom[bucket2/8] |= 1 << (bucket2 % 8)
}

// setPrefixBloomBit sets two bits in the prefix bloom filter for a key prefix.
func (w *sstWriter) setPrefixBloomBit(prefix []byte, size int) {
	h1 := fnv1aHash(prefix, fnv1aOffset32)
	h2 := fnv1aHash(prefix, fnv1aPrime32)
	bucket1 := int(h1) % size
	bucket2 := int(h2) % size
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

	checksum := crc32.Checksum(block, crc32.MakeTable(crc32.Koopman))
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
	if len(w.blocks) == 0 {
		return nil, nil
	}

	w.finishCurrentBlock()

	// R16-9: compute bloom size from final keyCount (10 bits/key
	// per ENG.md:91-92). Defer bit-set until now so the size is
	// known and we never have to re-hash on grow.
	bloomSize := bloomSizeFor(w.keyCount)
	w.bloom = make([]byte, bloomSize)
	for _, k := range w.keys {
		w.setBloomBitForSize(k, bloomSize)
	}

	// REQ000047: prefix bloom filter for range scans
	prefixBloomSize := bloomSizeFor(w.keyCount)
	w.prefixBloom = make([]byte, prefixBloomSize)
	for _, k := range w.keys {
		prefix := k
		if len(prefix) > 8 {
			prefix = prefix[:8]
		}
		w.setPrefixBloomBit(prefix, prefixBloomSize)
	}

	var buf bytes.Buffer

	// REQ000271: compress blocks and update index entries with compressed sizes
	compressedOffsets := make([]int, len(w.blocks))
	for i, block := range w.blocks {
		compressedOffsets[i] = buf.Len()
		compressed, err := compressBlock(block)
		if err != nil {
			return nil, err
		}
		buf.Write(compressed)
	}
	// Update index entries with compressed offsets and sizes
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

	indexOffset := buf.Len()

	for _, entry := range w.indexEntries {
		keyLen := encodeVarint(int64(len(entry.largestKey)))
		buf.Write(keyLen)
		buf.Write(entry.largestKey)
		offsetBuf := make([]byte, 8)
		binary.LittleEndian.PutUint64(offsetBuf, uint64(entry.blockOffset))
		buf.Write(offsetBuf)
		sizeBuf := make([]byte, 8)
		binary.LittleEndian.PutUint64(sizeBuf, uint64(entry.blockSize))
		buf.Write(sizeBuf)
	}

	indexSize := buf.Len() - indexOffset

	bloomOffset := buf.Len()
	buf.Write(w.bloom)

	buf.Write(w.prefixBloom)

	footer := make([]byte, sstFooterSize)
	binary.LittleEndian.PutUint64(footer[0:8], uint64(indexOffset))
	binary.LittleEndian.PutUint32(footer[8:12], uint32(indexSize))
	binary.LittleEndian.PutUint64(footer[12:20], uint64(bloomOffset))
	binary.LittleEndian.PutUint32(footer[20:24], uint32(bloomSize))
	binary.LittleEndian.PutUint32(footer[24:28], sstMagic)
	buf.Write(footer)

	return buf.Bytes(), nil
}

func (w *sstWriter) Reset() {
	w.blocks = w.blocks[:0]
	w.indexEntries = w.indexEntries[:0]
	w.keys = w.keys[:0]
	w.bloom = nil
	w.prefixBloom = nil
	w.keyCount = 0
	w.minKey = w.minKey[:0]
	w.maxKey = w.maxKey[:0]
}

// compressBlock compresses a data block using flate (REQ000271).
// Format: [1B flag (0=uncompressed, 1=compressed)] [data]
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
	// Only use compression if it actually saves space
	compressed := buf.Bytes()
	if len(compressed) < len(block)+1 {
		return compressed, nil
	}
	// Store uncompressed with flag=0
	out := make([]byte, 0, len(block)+1)
	out = append(out, 0) // uncompressed flag
	out = append(out, block...)
	return out, nil
}

// decompressBlock decompresses a data block (exported for reader).
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
