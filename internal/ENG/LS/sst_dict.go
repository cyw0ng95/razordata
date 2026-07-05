package ls

import (
	"bytes"
	"cmp"
	"compress/flate"
	"encoding/binary"
	"slices"
	"sync"
)

// dictTrainer builds a frequency-based dictionary for SST compression (REQ000297).
type dictTrainer struct {
	maxDictSize int
}

func newDictTrainer(maxDictSize int) *dictTrainer {
	if maxDictSize <= 0 {
		maxDictSize = 4096
	}
	return &dictTrainer{maxDictSize: maxDictSize}
}

func (dt *dictTrainer) train(block []byte) []byte {
	if len(block) < 8 {
		return nil
	}
	type pair struct {
		s string
		n int
	}
	counts := make(map[string]int, 256)
	const minLen, maxLen = 4, 8
	for l := minLen; l <= maxLen; l++ {
		if len(block) < l {
			break
		}
		for i := 0; i+l <= len(block); i++ {
			s := string(block[i : i+l])
			counts[s]++
		}
	}
	pairs := make([]pair, 0, len(counts))
	for s, n := range counts {
		if n < 2 {
			continue // ignore substrings that occur only once
		}
		pairs = append(pairs, pair{s, n})
	}
	slices.SortFunc(pairs, func(a, b pair) int { return cmp.Compare(b.n, a.n) })
	var dict bytes.Buffer
	dict.Grow(dt.maxDictSize)
	for _, p := range pairs {
		if dict.Len()+len(p.s) > dt.maxDictSize {
			break
		}
		dict.WriteString(p.s)
	}
	return dict.Bytes()
}

func compressBlockDict(block []byte) ([]byte, error) {
	return compressBlockDictShared(block, nil)
}

// compressBlockDictShared compresses block using a shared SST-level
// dictionary (REQ000587). Falls back to per-block training when
// sharedDict is nil or empty.
func compressBlockDictShared(block, sharedDict []byte) ([]byte, error) {
	if len(block) == 0 {
		return block, nil
	}
	var dict []byte
	if len(sharedDict) > 0 {
		dict = sharedDict
	} else {
		dt := newDictTrainer(4096)
		dict = dt.train(block)
	}
	if len(dict) == 0 {
		return compressBlock(block)
	}
	var compressed bytes.Buffer
	compressed.Grow(len(block))
	w, err := flate.NewWriterDict(&compressed, flate.BestSpeed, dict)
	if err != nil {
		return compressBlock(block)
	}
	if _, err := w.Write(block); err != nil {
		_ = w.Close()
		return compressBlock(block)
	}
	if err := w.Close(); err != nil {
		return compressBlock(block)
	}
	if compressed.Len() >= len(block) {
		return compressBlock(block)
	}
	var out []byte
	out = append(out, 2)
	out = encodeVarintHelper(out, uint64(len(dict)))
	out = append(out, dict...)
	out = append(out, compressed.Bytes()...)
	return out, nil
}

// trainSSTDict trains a shared dictionary from SST blocks (REQ000587).
func trainSSTDict(blocks [][]byte, maxDictSize int) []byte {
	if len(blocks) == 0 {
		return nil
	}
	dt := newDictTrainer(maxDictSize)
	sample := blocks
	if len(sample) > 8 {
		step := len(sample) / 8
		if step == 0 {
			step = 1
		}
		reduced := make([][]byte, 0, 8)
		for i := 0; i < len(sample) && len(reduced) < 8; i += step {
			reduced = append(reduced, sample[i])
		}
		sample = reduced
	}
	var total int
	for _, b := range sample {
		total += len(b)
	}
	corpus := make([]byte, 0, total)
	for _, b := range sample {
		corpus = append(corpus, b...)
	}
	return dt.train(corpus)
}

// decompressBlockDict decompresses a block produced by compressBlockDict.
func decompressBlockDict(data []byte) ([]byte, error) {
	if len(data) < 2 {
		return nil, ErrInvalidSSTFormat
	}
	flag := data[0]
	if flag == 0 {
		return data[1:], nil
	}
	if flag == 1 {
		return decompressFlateOnly(data[1:])
	}
	if flag != 2 {
		return nil, ErrInvalidSSTFormat
	}
	rest := data[1:]
	dictLen, n := binary.Uvarint(rest)
	if n <= 0 {
		return nil, ErrInvalidSSTFormat
	}
	rest = rest[n:]
	if int(dictLen) > len(rest) {
		return nil, ErrInvalidSSTFormat
	}
	dict := rest[:dictLen]
	rest = rest[dictLen:]
	return decompressFlateWithDict(rest, dict)
}

func decompressFlateOnly(data []byte) ([]byte, error) {
	// REQ001037: use pooled buffer to avoid per-block allocation.
	bufPtr := decompressBufPool.Get().(*bytes.Buffer)
	buf := bufPtr
	buf.Reset()
	r := flate.NewReader(bytes.NewReader(data))
	defer r.Close()
	if _, err := buf.ReadFrom(r); err != nil {
		buf.Reset()
		decompressBufPool.Put(bufPtr)
		return nil, err
	}
	result := decompressSlicePoolGet(buf.Len())
	copy(result, buf.Bytes())
	buf.Reset()
	decompressBufPool.Put(bufPtr)
	return result, nil
}

func decompressFlateWithDict(data, dict []byte) ([]byte, error) {
	// REQ001037: use pooled buffer to avoid per-block allocation.
	bufPtr := decompressBufPool.Get().(*bytes.Buffer)
	buf := bufPtr
	buf.Reset()
	r := flate.NewReaderDict(bytes.NewReader(data), dict)
	defer r.Close()
	if _, err := buf.ReadFrom(r); err != nil {
		buf.Reset()
		decompressBufPool.Put(bufPtr)
		return nil, err
	}
	result := decompressSlicePoolGet(buf.Len())
	copy(result, buf.Bytes())
	buf.Reset()
	decompressBufPool.Put(bufPtr)
	return result, nil
}

// REQ001037: pool for decompression output buffers.
var decompressBufPool = sync.Pool{
	New: func() any {
		return &bytes.Buffer{}
	},
}

// REQ001243: pool for decompressed block output slices.
// Each block is ~4 KB uncompressed. Pool returns slices with cap >= n.
var decompressSlicePool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 64*1024) // 64 KB initial capacity
		return &b
	},
}

func decompressSlicePoolGet(n int) []byte {
	bp := decompressSlicePool.Get().(*[]byte)
	b := *bp
	if cap(b) >= n {
		return b[:n]
	}
	// Pool slice too small — allocate fresh and discard the old one.
	return make([]byte, n)
}

// decompressSlicePoolPut returns a slice to the pool.
// The caller must NOT retain or reference the slice after calling this.
func decompressSlicePoolPut(b []byte) {
	if cap(b) == 0 {
		return
	}
	b = b[:0]
	decompressSlicePool.Put(&b)
}

var _ = binary.MaxVarintLen64
var _ = encodeVarintHelper

func encodeVarintHelper(dst []byte, v uint64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	return append(dst, tmp[:n]...)
}
