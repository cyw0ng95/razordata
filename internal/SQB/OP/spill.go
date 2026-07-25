package OP

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// spill.go implements binary serialization of columnar UT.Batch values
// for the grace hash join's spill-to-disk path (REQ001999).
//
// Format (per batch, self-describing so any valid prefix is decodable):
//
//	magic        uint32 LE  (spillMagic = 0x4752484A "GRHJ")
//	version      uint16 LE  (spillVersion = 1)
//	nCols        uint16 LE
//	nRows        uint32 LE  (== batch.Size)
//	per-column header:
//	  nameLen    uint16 LE
//	  name       [nameLen]byte
//	  type       uint8       (LX.TokenType cast to uint8 after whitelist)
//	  hasNulls   uint8       (0 or 1)
//	  nullsBytes uint32 LE   (ceil(nRows/8) when hasNulls, else 0)
//	per-column data:
//	  INT/BIGINT: nRows * 8 bytes (int64 LE)
//	  FLOAT:      nRows * 8 bytes (float64 LE)
//	  BOOL:       nRows bytes (0/1)
//	  TEXT/VARCHAR/BLOB: nRows * (uint32 len + len bytes)
//	  + nulls bitmap (if hasNulls): nullsBytes bytes

const (
	spillMagic   uint32 = 0x4752484A // "GRHJ"
	spillVersion uint16 = 1
	spillBufSize        = 64 * 1024 // 64 KB — page-cache-friendly
)

// spillBufPool reuses 64 KB buffers for bufio.Reader/Writer to avoid
// per-spill-file allocations. REQ001999.
var spillBufPool = sync.Pool{
	New: func() any { b := make([]byte, spillBufSize); return &b },
}

func getSpillBuf() []byte  { return *spillBufPool.Get().(*[]byte) }
func putSpillBuf(b []byte) { spillBufPool.Put(&b) }

// errSpillUnsupportedType is returned when a column type is not supported
// by the spill format. The caller should fall back to VectorizedHashJoin.
var errSpillUnsupportedType = errors.New("spill: unsupported column type")

// isSpillSupportedType reports whether a column type can be serialized.
func isSpillSupportedType(t LX.TokenType) bool {
	switch t {
	case LX.T_INT_KW, LX.T_BIGINT,
		LX.T_FLOAT_KW,
		LX.T_BOOL,
		LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		return true
	}
	return false
}

// logicalColCount returns the number of meaningful columns in a batch.
// Pooled batches pre-allocate Cols to MaxColumns; only the first N have
// non-zero Type. Mirrors meaningfulCols in op_vec_join.go. REQ001999.
func logicalColCount(b *UT.Batch) int {
	n := 0
	for i := range b.Cols {
		if b.Cols[i].Type != 0 {
			n = i + 1
		}
	}
	return n
}

// writeBatch serializes one columnar batch to w. The batch's Sel vector
// is ignored — callers must compact before spilling. REQ001999.
func writeBatch(w io.Writer, b *UT.Batch) error {
	if b == nil {
		return errors.New("spill: nil batch")
	}
	nCols := logicalColCount(b)
	if nCols > 0xFFFF {
		return fmt.Errorf("spill: too many columns %d", nCols)
	}
	nRows := b.Size

	// Validate all column types up front so we don't write a partial batch.
	for i := 0; i < nCols; i++ {
		if !isSpillSupportedType(b.Cols[i].Type) {
			return fmt.Errorf("%w: %v", errSpillUnsupportedType, b.Cols[i].Type)
		}
	}

	var hdr [12]byte
	binary.LittleEndian.PutUint32(hdr[0:4], spillMagic)
	binary.LittleEndian.PutUint16(hdr[4:6], spillVersion)
	binary.LittleEndian.PutUint16(hdr[6:8], uint16(nCols))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(nRows))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("spill: write header: %w", err)
	}

	// Column headers. Iterate only the logical columns — pooled batches
	// pre-allocate Cols to MaxColumns, but only the first nCols have
	// non-zero Type. Writing the unused tail would desync the reader.
	for i := 0; i < nCols; i++ {
		c := &b.Cols[i]
		nameLen := len(c.Name)
		if nameLen > 0xFFFF {
			nameLen = 0xFFFF // truncate; name is cosmetic
		}
		var chdr [9]byte
		binary.LittleEndian.PutUint16(chdr[0:2], uint16(nameLen))
		chdr[2] = byte(c.Type)
		hasNulls := 0
		if c.Nulls != nil {
			hasNulls = 1
		}
		chdr[3] = byte(hasNulls)
		nullsBytes := uint32(0)
		if hasNulls == 1 {
			nullsBytes = uint32((nRows + 7) / 8)
		}
		binary.LittleEndian.PutUint32(chdr[4:8], nullsBytes)
		// chdr[8] is padding for alignment; unused.
		if _, err := w.Write(chdr[:8]); err != nil {
			return fmt.Errorf("spill: write col header: %w", err)
		}
		if nameLen > 0 {
			if _, err := w.Write([]byte(c.Name[:nameLen])); err != nil {
				return fmt.Errorf("spill: write col name: %w", err)
			}
		}
	}

	// Column data.
	for i := 0; i < nCols; i++ {
		if err := writeColumnData(w, &b.Cols[i], nRows); err != nil {
			return err
		}
	}

	return nil
}

// writeColumnData writes one column's data payload (values + nulls bitmap).
func writeColumnData(w io.Writer, c *UT.Column, nRows int) error {
	switch c.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if len(c.Data.Ints) < nRows {
			return fmt.Errorf("spill: int column has %d rows, want %d", len(c.Data.Ints), nRows)
		}
		buf := make([]byte, 8*nRows)
		for i := 0; i < nRows; i++ {
			binary.LittleEndian.PutUint64(buf[i*8:], uint64(c.Data.Ints[i]))
		}
		if _, err := w.Write(buf); err != nil {
			return fmt.Errorf("spill: write ints: %w", err)
		}

	case LX.T_FLOAT_KW:
		if len(c.Data.Floats) < nRows {
			return fmt.Errorf("spill: float column has %d rows, want %d", len(c.Data.Floats), nRows)
		}
		buf := make([]byte, 8*nRows)
		for i := 0; i < nRows; i++ {
			binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(c.Data.Floats[i]))
		}
		if _, err := w.Write(buf); err != nil {
			return fmt.Errorf("spill: write floats: %w", err)
		}

	case LX.T_BOOL:
		if len(c.Data.Bools) < nRows {
			return fmt.Errorf("spill: bool column has %d rows, want %d", len(c.Data.Bools), nRows)
		}
		buf := make([]byte, nRows)
		for i := 0; i < nRows; i++ {
			if c.Data.Bools[i] {
				buf[i] = 1
			}
		}
		if _, err := w.Write(buf); err != nil {
			return fmt.Errorf("spill: write bools: %w", err)
		}

	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if len(c.Data.Strs) < nRows {
			return fmt.Errorf("spill: str column has %d rows, want %d", len(c.Data.Strs), nRows)
		}
		var lenBuf [4]byte
		for i := 0; i < nRows; i++ {
			s := c.Data.Strs[i]
			binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(s)))
			if _, err := w.Write(lenBuf[:]); err != nil {
				return fmt.Errorf("spill: write str len: %w", err)
			}
			if len(s) > 0 {
				if _, err := w.Write([]byte(s)); err != nil {
					return fmt.Errorf("spill: write str data: %w", err)
				}
			}
		}
	}

	// Nulls bitmap.
	if c.Nulls != nil {
		nullsBytes := (nRows + 7) / 8
		buf := make([]byte, nullsBytes)
		for i := 0; i < nRows; i++ {
			if i < len(c.Nulls) && c.Nulls[i] {
				buf[i/8] |= 1 << (i % 8)
			}
		}
		if _, err := w.Write(buf); err != nil {
			return fmt.Errorf("spill: write nulls: %w", err)
		}
	}

	return nil
}

// colMeta is the per-column metadata read from the spill header.
// Used to avoid a second switch on type during data reading.
type colMeta struct {
	typ        LX.TokenType
	hasNulls   bool
	nullsBytes int
}

// readBatch deserializes one batch from r. Returns a pooled batch
// (Pooled=true) so the caller can Put() it. Returns (nil, nil) at EOF.
// REQ001999.
func readBatch(r io.Reader) (*UT.Batch, error) {
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil // clean EOF
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("spill: truncated header: %w", err)
		}
		return nil, fmt.Errorf("spill: read header: %w", err)
	}

	magic := binary.LittleEndian.Uint32(hdr[0:4])
	if magic != spillMagic {
		return nil, fmt.Errorf("spill: bad magic 0x%X, want 0x%X", magic, spillMagic)
	}
	version := binary.LittleEndian.Uint16(hdr[4:6])
	if version != spillVersion {
		return nil, fmt.Errorf("spill: bad version %d, want %d", version, spillVersion)
	}
	nCols := int(binary.LittleEndian.Uint16(hdr[6:8]))
	nRows := int(binary.LittleEndian.Uint32(hdr[8:12]))

	b := UT.GetBatch(nCols)
	b.Size = nRows
	b.Pooled = true

	// Read column headers.
	metas := make([]colMeta, nCols)
	for i := 0; i < nCols; i++ {
		var chdr [8]byte
		if _, err := io.ReadFull(r, chdr[:]); err != nil {
			b.Put()
			return nil, fmt.Errorf("spill: read col header %d: %w", i, err)
		}
		nameLen := int(binary.LittleEndian.Uint16(chdr[0:2]))
		typ := LX.TokenType(chdr[2])
		if !isSpillSupportedType(typ) {
			b.Put()
			return nil, fmt.Errorf("%w: %v", errSpillUnsupportedType, typ)
		}
		hasNulls := chdr[3] != 0
		nullsBytes := int(binary.LittleEndian.Uint32(chdr[4:8]))

		var name string
		if nameLen > 0 {
			nameBuf := make([]byte, nameLen)
			if _, err := io.ReadFull(r, nameBuf); err != nil {
				b.Put()
				return nil, fmt.Errorf("spill: read col name %d: %w", i, err)
			}
			name = string(nameBuf)
		}
		b.Cols[i].Name = name
		b.Cols[i].Type = typ
		b.SetColumnName(i, name)
		metas[i] = colMeta{typ: typ, hasNulls: hasNulls, nullsBytes: nullsBytes}

		// Pre-allocate data slices.
		allocateColData(&b.Cols[i], nRows, typ)
	}

	// Read column data.
	for i := 0; i < nCols; i++ {
		if err := readColumnData(r, &b.Cols[i], nRows, metas[i]); err != nil {
			b.Put()
			return nil, err
		}
	}

	// Build colMap for O(1) name lookup (VectorizedHashJoin may need it).
	if nCols > 0 {
		colMap := make(map[string]int, nCols)
		for i, c := range b.Cols {
			if c.Name != "" {
				colMap[c.Name] = i
			}
		}
		b.SetColMap(colMap)
	}

	return b, nil
}

// readColumnData reads one column's data payload.
func readColumnData(r io.Reader, c *UT.Column, nRows int, meta colMeta) error {
	switch meta.typ {
	case LX.T_INT_KW, LX.T_BIGINT:
		buf := make([]byte, 8*nRows)
		if _, err := io.ReadFull(r, buf); err != nil {
			return fmt.Errorf("spill: read ints: %w", err)
		}
		for i := 0; i < nRows; i++ {
			c.Data.Ints[i] = int64(binary.LittleEndian.Uint64(buf[i*8:]))
		}

	case LX.T_FLOAT_KW:
		buf := make([]byte, 8*nRows)
		if _, err := io.ReadFull(r, buf); err != nil {
			return fmt.Errorf("spill: read floats: %w", err)
		}
		for i := 0; i < nRows; i++ {
			c.Data.Floats[i] = math.Float64frombits(binary.LittleEndian.Uint64(buf[i*8:]))
		}

	case LX.T_BOOL:
		buf := make([]byte, nRows)
		if _, err := io.ReadFull(r, buf); err != nil {
			return fmt.Errorf("spill: read bools: %w", err)
		}
		for i := 0; i < nRows; i++ {
			c.Data.Bools[i] = buf[i] != 0
		}

	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		var lenBuf [4]byte
		for i := 0; i < nRows; i++ {
			if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
				return fmt.Errorf("spill: read str len %d: %w", i, err)
			}
			sLen := int(binary.LittleEndian.Uint32(lenBuf[:]))
			if sLen > 0 {
				sBuf := make([]byte, sLen)
				if _, err := io.ReadFull(r, sBuf); err != nil {
					return fmt.Errorf("spill: read str data %d: %w", i, err)
				}
				c.Data.Strs[i] = string(sBuf)
			}
		}
	}

	// Nulls bitmap.
	if meta.hasNulls {
		buf := make([]byte, meta.nullsBytes)
		if _, err := io.ReadFull(r, buf); err != nil {
			return fmt.Errorf("spill: read nulls: %w", err)
		}
		c.Nulls = make([]bool, nRows)
		for i := 0; i < nRows; i++ {
			if buf[i/8]&(1<<(i%8)) != 0 {
				c.Nulls[i] = true
			}
		}
	}

	return nil
}

// spillFile wraps a temp file with a buffered writer for append-style
// batch writes and a buffered reader for sequential replay.
// REQ001999.
type spillFile struct {
	path    string
	f       *os.File
	w       *bufio.Writer
	buf     []byte // pooled buffer for the writer
	sealed  bool   // writer flushed + closed
	rClosed bool
}

// newSpillFile creates a temp file in dir (or os.TempDir() if dir == "").
// The file is opened read/write so it can be sealed and then replayed.
func newSpillFile(dir string) (*spillFile, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "razor-spill-*.bin")
	if err != nil {
		return nil, fmt.Errorf("spill: create temp file: %w", err)
	}
	buf := getSpillBuf()
	return &spillFile{
		path: f.Name(),
		f:    f,
		w:    bufio.NewWriterSize(f, spillBufSize),
		buf:  buf,
	}, nil
}

// WriteBatch appends one batch to the spill file. REQ001999.
func (s *spillFile) WriteBatch(b *UT.Batch) error {
	if s.sealed {
		return errors.New("spill: write after seal")
	}
	return writeBatch(s.w, b)
}

// Seal flushes the buffered writer. After Seal, no more writes are allowed.
// The underlying file handle is kept open for the reader phase. REQ001999.
func (s *spillFile) Seal() error {
	if s.sealed {
		return nil
	}
	if err := s.w.Flush(); err != nil {
		return fmt.Errorf("spill: flush: %w", err)
	}
	s.sealed = true
	return nil
}

// OpenReader seeks to the beginning of the file and returns a buffered
// reader. The caller must Close the returned reader when done. REQ001999.
func (s *spillFile) OpenReader() (io.ReadCloser, error) {
	if !s.sealed {
		if err := s.Seal(); err != nil {
			return nil, err
		}
	}
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("spill: seek: %w", err)
	}
	return &spillReader{f: s.f, r: bufio.NewReaderSize(s.f, spillBufSize)}, nil
}

// spillReader wraps the file + buffered reader and closes both on Close.
type spillReader struct {
	f *os.File
	r *bufio.Reader
}

func (sr *spillReader) Read(p []byte) (int, error) { return sr.r.Read(p) }
func (sr *spillReader) Close() error {
	if sr.f != nil {
		return sr.f.Close()
	}
	return nil
}

// Close removes the temp file and releases the pooled buffer.
// Idempotent. REQ001999.
func (s *spillFile) Close() error {
	if s.buf != nil {
		putSpillBuf(s.buf)
		s.buf = nil
	}
	if s.f != nil {
		_ = s.f.Close()
		s.f = nil
	}
	if s.path != "" {
		_ = os.Remove(s.path)
		s.path = ""
	}
	return nil
}

// Path returns the temp file path (for testing/diagnostics).
func (s *spillFile) Path() string { return s.path }

// ensureTempDir creates a temp directory for spill files under dir
// (or os.TempDir() if dir == "") and returns its path. REQ001999.
func ensureTempDir(dir string) (string, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	d, err := os.MkdirTemp(dir, "razor-grace-*")
	if err != nil {
		return "", fmt.Errorf("spill: mkdir temp: %w", err)
	}
	return d, nil
}

// removeTempDir removes a temp directory and all its contents. REQ001999.
func removeTempDir(dir string) {
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// filepath import is used by ensureTempDir's caller in grace_hashjoin.go
// for joining partition-specific paths; keep the import alive.
var _ = filepath.Join
