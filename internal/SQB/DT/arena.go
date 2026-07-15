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
	slabs   [][]Value // all slabs ever allocated — kept alive for GC tracing
	slab    []Value   // current active slab
	offset  int
	slabCap int
	// REQ001424: initialized is set after the first Init so writer code
	// can skip redundant Init() calls when the same arena is reused
	// across multiple UPDATE/DELETE statements.
	initialized bool
	// REQ001496: pooledSlab holds a slab returned to the pool for
	// the next grow() call, avoiding a pool Get/Put round-trip.
	pooledSlab []Value
}

// Init pre-allocates a slab large enough for estimatedRows rows of
// colsPerRow columns, reducing the number of grow() calls during
// execution. REQ001260.
func (a *RowArena) Init(estimatedRows, colsPerRow int) {
	a.initialized = true
	needed := estimatedRows * colsPerRow
	if needed <= 0 {
		return
	}
	// Add 25% margin for overhead.
	needed = needed + needed/4
	if needed < arenaSlabSize {
		needed = arenaSlabSize
	}
	// REQ001496: reuse pooled slab if capacity suffices.
	if needed <= arenaSlabSize && a.pooledSlab != nil && cap(a.pooledSlab) >= needed {
		a.slab = a.pooledSlab[:needed:needed]
		a.pooledSlab = nil
	} else {
		a.slab = getSlab(needed)
	}
	a.offset = 0
	a.slabCap = len(a.slab)
}

// Initialized reports whether Init has been called on this arena.
func (a *RowArena) Initialized() bool { return a.initialized }

func (a *RowArena) Reset() {
	// Let GC collect slabs naturally. Old Row.Data sub-slices
	// created by AllocRow keep the slabs alive through normal
	// GC tracing — no unsafe.Pointer overlay needed.
	a.initialized = false
	a.slabs = a.slabs[:0]
	// REQ001496: return the current slab to the pool if it fits.
	if a.slab != nil && cap(a.slab) <= arenaSlabSize {
		a.pooledSlab = a.slab
		putSlab(a.slab)
	}
	a.slab = nil
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
	needed := a.offset + nCols
	if needed > a.slabCap {
		a.grow(needed)
	}
	start := a.offset
	a.offset += nCols
	return &Row{
		Cols:     schema.Cols,
		Data:     a.slab[start : start+nCols : start+nCols],
		ColIndex: schema.ColIndex,
	}
}

// BumpValues reserves `n` Value slots in the current slab and
// returns the slot offset. Used by callers that want to fill the
// slots directly without going through AllocRow's schema-bound
// Row allocation. REQ001426 (INSERT batch arena path).
func (a *RowArena) BumpValues(n int) (int, bool) {
	if n <= 0 {
		return 0, true
	}
	needed := a.offset + n
	if needed > a.slabCap {
		a.grow(needed)
	}
	start := a.offset
	a.offset += n
	return start, true
}

// SliceAt returns the value-slice covering the given offset + length.
// Caller is responsible for the offset being in range.
func (a *RowArena) SliceAt(start, n int) []Value {
	return a.slab[start : start+n : start+n]
}

func (a *RowArena) grow(needed int) {
	if a.slab != nil {
		a.slabs = append(a.slabs, a.slab) // keep alive for GC tracing
	}
	// REQ001285: geometric growth — double the slab each time to reduce
	// grow() frequency. For 10K rows x 6 cols x 48B = 2.88MB, fixed
	// 64KB slabs require ~44 grows; geometric doubling needs ~6.
	cap := arenaSlabSize
	if a.slabCap > 0 {
		cap = a.slabCap * 2
	}
	if needed > cap {
		cap = needed
	}
	// REQ001496: reuse pooled slab if available.
	if cap <= arenaSlabSize && a.pooledSlab != nil {
		a.slab = a.pooledSlab[:cap:cap]
		a.pooledSlab = nil
	} else {
		a.slab = getSlab(cap)
	}
	a.offset = 0
	a.slabCap = len(a.slab)
}

const arenaSlabSize = 64 * 1024 / int(unsafe.Sizeof(Value{})) // ~8K Values per slab

// arenaSlabPool pools []Value slabs across RowArena instances to
// eliminate per-query allocation. REQ001496.
var arenaSlabPool = sync.Pool{
	New: func() any { return make([]Value, arenaSlabSize) },
}

// getSlab returns a zeroed []Value of at least cap capacity from the pool.
func getSlab(cap int) []Value {
	if cap <= arenaSlabSize {
		v := arenaSlabPool.Get().([]Value)
		return v[:cap:cap]
	}
	// For larger capacities, allocate directly.
	return make([]Value, cap)
}

// putSlab returns a slab to the pool if it fits.
func putSlab(slab []Value) {
	if cap(slab) <= arenaSlabSize {
		for i := range slab {
			slab[i] = Value{}
		}
		arenaSlabPool.Put(slab[:arenaSlabSize])
	}
}

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
	needed := a.offset + n
	if needed > a.slabCap {
		a.grow(needed)
	}
	start := a.offset
	a.offset += n
	dst := a.slab[start : start+n : start+n]
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
