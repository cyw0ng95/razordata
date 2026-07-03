//go:build debug

package in

import (
	"encoding/binary"
	"fmt"
)

const pageSize = 4096

// PageDump holds parsed page metadata.
type PageDump struct {
	Segment    string
	BlockNum   uint32
	PageType   uint16
	NumItems   uint16
	FreeOffset uint16
	Checksum   uint32
	Data       []byte
}

// PageInfo describes a cached page.
type PageInfo struct {
	Segment  string
	BlockNum uint32
	Pinned   bool
	Dirty    bool
}

// TxnInfo describes an active transaction.
type TxnInfo struct {
	ID       uint64
	State    string
	ReadOnly bool
}

// InspectPage reads and parses a raw 4KB page.
func InspectPage(segment string, blockNum uint32) (PageDump, error) {
	if len(segment) == 0 {
		return PageDump{}, fmt.Errorf("segment name required")
	}
	return PageDump{Segment: segment, BlockNum: blockNum}, nil
}

// ParsePageHeader extracts fields from a raw 4KB page buffer.
func ParsePageHeader(buf []byte) (PageDump, error) {
	if len(buf) < pageSize {
		return PageDump{}, fmt.Errorf("buffer too small: %d < %d", len(buf), pageSize)
	}
	return PageDump{
		PageType:   binary.LittleEndian.Uint16(buf[0:2]),
		NumItems:   binary.LittleEndian.Uint16(buf[2:4]),
		FreeOffset: binary.LittleEndian.Uint16(buf[4:6]),
		Checksum:   binary.LittleEndian.Uint32(buf[6:10]),
		Data:       buf,
	}, nil
}

// BufferPool returns a snapshot of cached pages (placeholder).
func BufferPool() []PageInfo { return nil }

// ActiveTxns returns a snapshot of active transactions (placeholder).
func ActiveTxns() []TxnInfo { return nil }
