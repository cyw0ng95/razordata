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
	keyCount           int
	minKey             []byte
	maxKey             []byte
	lastKey            []byte
	currentBlockOffset int
}

func newSSTWriter() *sstWriter {
	bloomSize := 4096
	return &sstWriter{
		blocks:       make([][]byte, 0, 16),
		indexEntries: make([]indexEntry, 0),
		bloom:        make([]byte, bloomSize),
	}
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
	w.setBloomBit(key)
	w.lastKey = append(w.lastKey[:0], key...)
}

func (w *sstWriter) setBloomBit(key []byte) {
	hash1 := crc32.Checksum(key, crc32.MakeTable(crc32.Koopman))
	hash2 := crc32.Checksum(key, crc32.MakeTable(crc32.Castagnoli))

	size := len(w.bloom) * 8
	bucket1 := int(hash1) % size
	bucket2 := int(hash2) % size

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

	block = append(block, 0, 0, 0, 0)
	block = append(block, 1, 0, 0, 0)

	checksum := crc32.Checksum(block, crc32.MakeTable(crc32.Koopman))
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
	bloomSize := len(w.bloom)

	footer := make([]byte, sstFooterSize)
	binary.LittleEndian.PutUint64(footer[0:8], uint64(indexOffset))
	binary.LittleEndian.PutUint32(footer[8:12], uint32(indexSize))
	binary.LittleEndian.PutUint64(footer[12:20], uint64(bloomOffset))
	binary.LittleEndian.PutUint32(footer[20:24], uint32(bloomSize))
	binary.LittleEndian.PutUint32(footer[24:28], sstMagic)
	buf.Write(footer)

	return buf.Bytes(), nil
}

func findRestartPoints(block []byte, interval int) []int {
	points := make([]int, 0)
	for i := 0; i < len(block); {
		pos := bytes.LastIndex(block[:i], []byte{0, 0, 0, 0})
		if pos >= 0 {
			keyLen := binary.LittleEndian.Uint32(block[pos:])
			if keyLen == 0 {
				points = append(points, i)
			}
		}
		i += 4096
	}
	if len(points) == 0 && len(block) > 0 {
		points = append(points, 0)
	}
	return points
}

func extractLargestKey(block []byte) []byte {
	if len(block) < 8 {
		return nil
	}

	var pos int
	var fullKey []byte

	for pos < len(block) {
		keyLen, n := decodeVarint(block[pos:])
		if keyLen == 0 || pos+n+int(keyLen) > len(block) {
			break
		}
		pos += n
		fullKey = block[pos : pos+int(keyLen)]
		pos += int(keyLen)

		valueLen, n := decodeVarint(block[pos:])
		if valueLen == 0 || pos+n+int(valueLen) > len(block) {
			break
		}
		pos += n + int(valueLen)
	}

	return fullKey
}

func (w *sstWriter) Reset() {
	w.blocks = w.blocks[:0]
	w.indexEntries = w.indexEntries[:0]
	w.keyCount = 0
	w.minKey = w.minKey[:0]
	w.maxKey = w.maxKey[:0]
}
