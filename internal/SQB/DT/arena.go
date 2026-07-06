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
	s := make([]byte, needed)
	a.slab = s
	a.offset = 0
	a.slabCap = needed
}

func (a *RowArena) Reset() {
	for _, s := range a.slabs {
		arenaSlabPool.Put(s)
	}
	a.slabs = a.slabs[:0]
	if a.slab != nil {
		arenaSlabPool.Put(a.slab)
		a.slab = nil
	}
	a.offset = 0
	a.slabCap = 0
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

func (a *RowArena) grow(needed int) {
	if a.slab != nil {
		a.slabs = append(a.slabs, a.slab) // keep alive for GC tracing
	}
	cap := arenaSlabSize
	if needed > cap {
		cap = needed
		if cap < arenaSlabSize*2 {
			cap = arenaSlabSize * 2
		}
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
	colCount, err := readVarint()
	if err != nil {
		return err
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

var arenaSlabPool = sync.Pool{
	New: func() any {
		return make([]byte, arenaSlabSize)
	},
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
