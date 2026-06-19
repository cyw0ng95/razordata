package bf

import (
	"hash/maphash"
	"sync/atomic"
)

// wtinyLFU is a frequency-based admission policy (REQ000303).
type wtinyLFU struct {
	rows     [4][]uint8 // 4-bit counters packed 2 per byte
	rowSize  int
	seed     maphash.Seed
	window   []uint64 // small LRU of recent admissions; size = windowSize
	winHead  int
	winSize  int
	admitted uint64
	rejected uint64
}

const (
	wtinyLFUWindowSize = 100
	wtinyLFURows       = 4
	wtinyLFUCols       = 16384 // 2^14
)

func newWTinyLFU() *wtinyLFU {
	w := &wtinyLFU{
		rowSize: wtinyLFUCols / 2, // 2 counters per byte
		seed:    maphash.MakeSeed(),
		winSize: wtinyLFUWindowSize,
	}
	for i := range w.rows {
		w.rows[i] = make([]uint8, w.rowSize)
	}
	w.window = make([]uint64, wtinyLFUWindowSize)
	return w
}

func (w *wtinyLFU) estimate(key uint64) int {
	min := byte(255)
	for r := 0; r < wtinyLFURows; r++ {
		var h maphash.Hash
		h.SetSeed(w.seed)
		_, _ = h.Write(uint64ToBytes(key))
		_, _ = h.Write([]byte{byte(r)})
		idx := h.Sum64() & uint64(wtinyLFUCols-1)
		byteIdx := idx / 2
		shift := uint((idx & 1) * 4)
		c := (w.rows[r][byteIdx] >> shift) & 0x0F
		if c < min {
			min = c
		}
	}
	return int(min)
}

func (w *wtinyLFU) increment(key uint64) {
	for r := 0; r < wtinyLFURows; r++ {
		var h maphash.Hash
		h.SetSeed(w.seed)
		_, _ = h.Write(uint64ToBytes(key))
		_, _ = h.Write([]byte{byte(r)})
		idx := h.Sum64() & uint64(wtinyLFUCols-1)
		byteIdx := idx / 2
		shift := uint((idx & 1) * 4)
		c := (w.rows[r][byteIdx] >> shift) & 0x0F
		if c < 15 {
			w.rows[r][byteIdx] = (w.rows[r][byteIdx] &^ (0x0F << shift)) | ((c + 1) << shift)
		}
	}
}

// recordHit records an access for key. Returns true if hot.
func (w *wtinyLFU) recordHit(key uint64) bool {
	w.increment(key)
	return w.estimate(key) > 1
}

// admit runs the admission test: should the new key be admitted
// over the candidate key being evicted?
// Returns true (admit) if new key's frequency is at least as high
// as the candidate's. Otherwise false (reject).
func (w *wtinyLFU) admit(newKey, candidateKey uint64) bool {
	w.increment(newKey)
	newFreq := w.estimate(newKey)
	candFreq := w.estimate(candidateKey)
	// Track the recent window of admissions.
	w.window[w.winHead%w.winSize] = newKey
	w.winHead++
	if newFreq > candFreq {
		atomic.AddUint64(&w.admitted, 1)
		return true
	}
	atomic.AddUint64(&w.rejected, 1)
	return false
}

// stats returns a snapshot of admission statistics.
type wtinyLFUStats struct {
	Admitted uint64
	Rejected uint64
}

func (w *wtinyLFU) stats() wtinyLFUStats {
	return wtinyLFUStats{
		Admitted: atomic.LoadUint64(&w.admitted),
		Rejected: atomic.LoadUint64(&w.rejected),
	}
}

func uint64ToBytes(v uint64) []byte {
	return []byte{
		byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24),
		byte(v >> 32), byte(v >> 40), byte(v >> 48), byte(v >> 56),
	}
}
