package ls

import (
	"bytes"
	"encoding/binary"
	"errors"
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
// bloom filter of byte size size. The bucket positions are
// computed from size, so the keys collected during Add are now
// hashed into a bloom sized for the actual keyCount.
func (w *sstWriter) setBloomBitForSize(key []byte, size int) {
	hash1 := crc32.Checksum(key, crc32.MakeTable(crc32.Koopman))
	hash2 := crc32.Checksum(key, crc32.MakeTable(crc32.Castagnoli))

	bitCount := size * 8
	bucket1 := int(hash1) % bitCount
	bucket2 := int(hash2) % bitCount

	w.bloom[bucket1/8] |= 1 << (bucket1 % 8)
	w.bloom[bucket2/8] |= 1 << (bucket2 % 8)
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

	var buf bytes.Buffer

	for _, block := range w.blocks {
		buf.Write(block)
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
	w.keyCount = 0
	w.minKey = w.minKey[:0]
	w.maxKey = w.maxKey[:0]
}
