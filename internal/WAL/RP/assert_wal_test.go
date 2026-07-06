//go:build debug

package rp

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	sp "github.com/cyw0ng95/razordata/internal/MEM/SP"
	wr "github.com/cyw0ng95/razordata/internal/WAL/WR"
)

func TestWAL_Assert_MidSegmentCorrupt(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		tmp := t.TempDir()
		sm, err := setupSegmentManager(tmp)
		if err != nil {
			return
		}
		defer sm.Close()
		bp, err := setupBufferPool(tmp)
		if err != nil {
			return
		}
		defer bp.Close()

		w, err := wr.New(tmp, sm, sp.New(), nil, false)
		if err != nil {
			return
		}
		w.Append(&wr.WriteBatch{TxnID: 1, Recs: []wr.LogRecord{
			{Type: wr.RTData, BlockID: 1, Value: []byte("payload")},
		}})
		w.Sync()
		w.Close()

		path := filepath.Join(tmp, "wal", "wal.000")
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}

		bodyStart := int64(wr.WALHeaderSize)
		length, n := binary.Uvarint(data[bodyStart:])
		if n <= 0 {
			return
		}
		bodyOff := bodyStart + int64(n)
		totalLen := int64(length)
		bodyEnd := bodyOff + totalLen - 4
		if bodyEnd <= bodyOff+15 {
			return
		}
		data[bodyOff+15] ^= 0xFF
		body := data[bodyOff:bodyEnd]
		newCRC := crc32.ChecksumIEEE(body)
		crcOff := bodyEnd
		binary.LittleEndian.PutUint32(data[crcOff:crcOff+4], newCRC)
		os.WriteFile(path, data, 0o644)

		r, err := New(tmp, sm, bp, Callbacks{}, nil)
		if err != nil {
			return
		}
		defer r.Close()
		r.Replay()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestWAL_Assert_MidSegmentCorrupt")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatal("expected BUG_ON to exit(1), but it didn't")
}