package DT

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"unsafe"
)

type RowArena struct {
	slabs   [][]byte // all slabs ever allocated — kept alive for GC tracing
	slab    []byte   // current active slab
	offset  int
	slabCap int
}

// Init pre-allocates a slab large enough for estimatedRows rows of
// colsPerRow columns, reducing the number of grow() calls during
// execution. REQ001260.
func (a *RowArena) Init(estimatedRows, colsPerRow int) {
	needed := estimatedRows * colsPerRow * valueSize
	if needed <= 0 {
		return
	}
	// Add 25% margin for overhead.
	needed = needed + needed/4
	if needed < arenaSlabSize {
		needed = arenaSlabSize
	}
	// REQ001289: try size-bucketed cache first before allocating fresh.
	a.slab = getSlab(needed)
	a.offset = 0
	a.slabCap = len(a.slab)
}

func (a *RowArena) Reset() {
	for _, s := range a.slabs {
		putSlab(s)
	}
	a.slabs = a.slabs[:0]
	if a.slab != nil {
		putSlab(a.slab)
		a.slab = nil
	}
	a.offset = 0
	a.slabCap = 0
}

// ResetOffset resets the bump pointer without returning the slab to the
// cache. Used by REQ001419 (persistent Engine arena) to reuse the same
// slab across queries without per-query allocation.
func (a *RowArena) ResetOffset() {
	a.offset = 0
}

func (a *RowArena) AllocRow(nCols int, schema *StoreSchema) *Row {
	if nCols <= 0 {
		return &Row{
			Cols:     schema.Cols,
			ColIndex: schema.ColIndex,
		}
	}
	needed := a.offset + nCols*valueSize
	if needed > a.slabCap {
		a.grow(needed)
	}
	start := a.offset
	a.offset += nCols * valueSize
	return &Row{
		Cols:     schema.Cols,
		Data:     (*[1 << 30]Value)(unsafe.Pointer(&a.slab[start]))[:nCols:nCols],
		ColIndex: schema.ColIndex,
	}
}

// BumpValues reserves `n` Value slots in the current slab and
// returns the byte offset. Used by callers that want to fill the
// slots directly without going through AllocRow's schema-bound
// Row allocation. REQ001426 (INSERT batch arena path).
func (a *RowArena) BumpValues(n int) (int, bool) {
	if n <= 0 {
		return 0, true
	}
	needed := a.offset + n*valueSize
	if needed > a.slabCap {
		a.grow(needed)
	}
	start := a.offset
	a.offset += n * valueSize
	return start, true
}

// SliceAt returns the value-slice covering the given byte offset
// + length. Caller is responsible for the offset being in range.
func (a *RowArena) SliceAt(start, n int) []Value {
	return (*[1 << 30]Value)(unsafe.Pointer(&a.slab[start]))[:n:n]
}

func (a *RowArena) grow(needed int) {
	if a.slab != nil {
		a.slabs = append(a.slabs, a.slab) // keep alive for GC tracing
	}
	// REQ001285: geometric growth — double the slab each time to reduce
	// grow() frequency. For 10K rows × 6 cols × 48B = 2.88MB, fixed
	// 64KB slabs require ~44 grows; geometric doubling needs ~6.
	cap := arenaSlabSize
	if a.slabCap > 0 {
		cap = a.slabCap * 2
	}
	if needed > cap {
		cap = needed
	}
	s := make([]byte, cap)
	a.slab = s
	a.offset = 0
	a.slabCap = cap
}

const arenaSlabSize = 64 * 1024
const valueSize = int(unsafe.Sizeof(Value{}))

func DecodeRowInto(row *Row, data []byte, schema *StoreSchema) error {
	nCols := len(schema.Cols)
	if len(row.Data) != nCols {
		return fmt.Errorf("DT: row has %d cols, schema %d", len(row.Data), nCols)
	}
	if data == nil || len(data) == 0 {
		return errors.New("DT: empty row payload")
	}
	off := 0
	readVarint := func() (uint64, error) {
		v, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return 0, errors.New("DT: bad varint")
		}
		off += n
		return v, nil
	}
	// REQ001279: fixed-width column count for tables < 255 columns.
	// First byte < 255 is the column count directly; 255 escapes to varint.
	colCount := uint64(data[off])
	off++
	if colCount == 255 {
		var err error
		colCount, err = readVarint()
		if err != nil {
			return err
		}
	}
	if int(colCount) != nCols {
		return fmt.Errorf("DT: row has %d cols, schema %d", colCount, nCols)
	}
	for i := 0; i < nCols; i++ {
		if off >= len(data) {
			return errors.New("DT: truncated row")
		}
		tag := data[off]
		off++
		switch tag {
		case rvNull:
			row.Data[i] = NullValue()
		case rvInt:
			if off+8 > len(data) {
				return errors.New("DT: truncated int")
			}
			row.Data[i] = NewIntValue(int64(binary.BigEndian.Uint64(data[off : off+8])))
			off += 8
		case rvFloat:
			if off+8 > len(data) {
				return errors.New("DT: truncated float")
			}
			row.Data[i] = NewFloatValue(math.Float64frombits(binary.BigEndian.Uint64(data[off : off+8])))
			off += 8
		case rvBool:
			if off+1 > len(data) {
				return errors.New("DT: truncated bool")
			}
			row.Data[i] = NewBoolValue(data[off] != 0)
			off++
		case rvString:
			l, _ := readVarint()
			if off+int(l) > len(data) {
				return errors.New("DT: truncated string")
			}
			row.Data[i] = NewTextValue(string(data[off : off+int(l)]))
			off += int(l)
		case rvBytes:
			l, _ := readVarint()
			if off+int(l) > len(data) {
				return errors.New("DT: truncated bytes")
			}
			row.Data[i] = NewBlobValue(append([]byte{}, data[off:off+int(l)]...))
			off += int(l)
		default:
			return fmt.Errorf("DT: unknown row tag %d", tag)
		}
	}
	return nil
}

// sizeBuckets defines the fixed slab size classes for the pool.
// REQ001289: size-bucketed slab cache to avoid re-allocating large slabs on Init.
var sizeBuckets = []int{
	64 * 1024,       // 64 KB
	256 * 1024,      // 256 KB
	1024 * 1024,     // 1 MB
	4 * 1024 * 1024, // 4 MB
}

// sizeBucketIndex returns the index of the smallest bucket >= n.
// Returns -1 if n exceeds the largest bucket.
func sizeBucketIndex(n int) int {
	for i, sz := range sizeBuckets {
		if n <= sz {
			return i
		}
	}
	return -1
}

// slabStack is a LIFO stack of slabs for one size class.
type slabStack struct {
	slabs [][]byte
}

// slabCache is a size-bucketed manual slab cache keyed by size class.
// Replaces sync.Pool which drops items between GC cycles. REQ001290.
var slabCache struct {
	mu        sync.Mutex
	stacks    [4]slabStack
	total     int64
	highWater int64
}

const defaultHighWater = 16 * 1024 * 1024 // 16 MB

func init() {
	slabCache.highWater = defaultHighWater
}

// getSlab returns a slab from the cache with capacity >= minSize,
// or allocates a fresh one if no cached slab is large enough.
func getSlab(minSize int) []byte {
	idx := sizeBucketIndex(minSize)
	if idx < 0 {
		return make([]byte, minSize)
	}
	slabCache.mu.Lock()
	st := &slabCache.stacks[idx]
	if len(st.slabs) > 0 {
		last := len(st.slabs) - 1
		buf := st.slabs[last]
		st.slabs = st.slabs[:last]
		slabCache.total -= int64(cap(buf))
		slabCache.mu.Unlock()
		return buf
	}
	slabCache.mu.Unlock()
	return make([]byte, sizeBuckets[idx])
}

// putSlab returns a slab to the appropriate size bucket.
// Trims oldest slabs when total exceeds high-water mark.
func putSlab(slab []byte) {
	n := cap(slab)
	idx := sizeBucketIndex(n)
	if idx < 0 {
		return // too large, let GC handle it
	}
	slabCache.mu.Lock()
	slabCache.total += int64(n)
	st := &slabCache.stacks[idx]
	st.slabs = append(st.slabs, slab[:n])
	// Trim oldest slabs when high-water mark exceeded.
	for slabCache.total > slabCache.highWater {
		// Find the size class with the most slabs.
		maxIdx := 0
		maxLen := len(slabCache.stacks[0].slabs)
		for i := 1; i < 4; i++ {
			if len(slabCache.stacks[i].slabs) > maxLen {
				maxIdx = i
				maxLen = len(slabCache.stacks[i].slabs)
			}
		}
		if maxLen == 0 {
			break
		}
		st := &slabCache.stacks[maxIdx]
		last := len(st.slabs) - 1
		discarded := cap(st.slabs[last])
		st.slabs = st.slabs[:last]
		slabCache.total -= int64(discarded)
	}
	slabCache.mu.Unlock()
}

// CloneRow clones an existing row into the arena. The returned row
// has Cols/Types/ColIndex shared with the input, but Data is copied
// into the arena's bump allocator. REQ001233.
func (a *RowArena) CloneRow(r Row) Row {
	if r.Data == nil {
		return r
	}
	n := len(r.Data)
	if n == 0 {
		return r
	}
	needed := a.offset + n*valueSize
	if needed > a.slabCap {
		a.grow(needed)
	}
	start := a.offset
	a.offset += n * valueSize
	dst := (*[1 << 30]Value)(unsafe.Pointer(&a.slab[start]))[:n:n]
	for i := 0; i < n; i++ {
		dst[i] = r.Data[i]
	}
	return Row{
		Cols:     r.Cols,
		Types:    r.Types,
		ColIndex: r.ColIndex,
		Data:     dst,
	}
}

// CloneRowsBatch clones multiple rows into the arena. Returns a slice
// of cloned rows. All rows share the same Cols/Types slices (from the
// first row) and Data slices are copied into the arena. REQ001233.
func (a *RowArena) CloneRowsBatch(rows []Row) []Row {
	if len(rows) == 0 {
		return nil
	}
	// Shared Cols/Types from first row
	sharedCols := rows[0].Cols
	sharedTypes := rows[0].Types
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		cloned := a.CloneRow(r)
		cloned.Cols = sharedCols
		cloned.Types = sharedTypes
		out = append(out, cloned)
	}
	return out
}
