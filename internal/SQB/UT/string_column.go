package UT

import "unsafe"

// StringColumn provides an optimized storage layout for string
// columns (REQ000546). Instead of storing []string (which has
// 16-byte string headers per element), it stores strings as a
// single []byte buffer with a parallel []uint32 offset array.
//
// This eliminates string-header copy overhead and enables
// memcmp-based string comparison (future SIMD optimization).
//
// Memory layout:
//   - Data: []byte — concatenated string bytes
//   - Offsets: []uint32 — start offset of each string in Data
//   - Lengths: []uint32 — length of each string
//
// For BatchSize=1024 strings of average 32 bytes:
//   - []string: 1024 * 16 = 16 KiB headers + string data
//   - StringColumn: 1024 * 8 = 8 KiB offsets/lengths + string data
//
// Backward compatibility: Use StringValue(col, row) to access
// strings in a type-safe manner.

// StringColumn stores strings in a columnar format.
type StringColumn struct {
	Data    []byte   // concatenated string bytes
	Offsets []uint32 // start offset of each string
	Lengths []uint32 // length of each string
	Nulls   []bool   // null flags
	Size    int      // number of strings
}

// NewStringColumn creates a StringColumn with capacity for n strings.
func NewStringColumn(n int) *StringColumn {
	return &StringColumn{
		Data:    make([]byte, 0, n*32), // estimate 32 bytes per string
		Offsets: make([]uint32, n),
		Lengths: make([]uint32, n),
		Nulls:   make([]bool, n),
		Size:    0,
	}
}

// Append adds a string to the column.
func (sc *StringColumn) Append(s string, null bool) {
	idx := sc.Size
	if idx >= len(sc.Offsets) {
		return
	}
	sc.Nulls[idx] = null
	if null {
		sc.Offsets[idx] = uint32(len(sc.Data))
		sc.Lengths[idx] = 0
	} else {
		sc.Offsets[idx] = uint32(len(sc.Data))
		sc.Lengths[idx] = uint32(len(s))
		sc.Data = append(sc.Data, s...)
	}
	sc.Size++
}

// StringValue returns the string at row idx. Returns "" for null.
func (sc *StringColumn) StringValue(idx int) string {
	if idx < 0 || idx >= sc.Size || sc.Nulls[idx] {
		return ""
	}
	off := sc.Offsets[idx]
	length := sc.Lengths[idx]
	// Unsafe conversion to avoid copy — the caller must not
	// modify the returned string while the column is alive.
	return unsafe.String(&sc.Data[off], length)
}

// IsNull returns true if the value at row idx is null.
func (sc *StringColumn) IsNull(idx int) bool {
	if idx < 0 || idx >= sc.Size {
		return true
	}
	return sc.Nulls[idx]
}

// Compare compares strings at rows i and j without allocating.
// Returns -1, 0, or 1.
func (sc *StringColumn) Compare(i, j int) int {
	if sc.Nulls[i] && sc.Nulls[j] {
		return 0
	}
	if sc.Nulls[i] {
		return -1
	}
	if sc.Nulls[j] {
		return 1
	}
	li := sc.Lengths[i]
	lj := sc.Lengths[j]
	minLen := li
	if lj < minLen {
		minLen = lj
	}
	oi := sc.Offsets[i]
	oj := sc.Offsets[j]
	for k := uint32(0); k < minLen; k++ {
		if sc.Data[oi+k] < sc.Data[oj+k] {
			return -1
		}
		if sc.Data[oi+k] > sc.Data[oj+k] {
			return 1
		}
	}
	if li < lj {
		return -1
	}
	if li > lj {
		return 1
	}
	return 0
}
