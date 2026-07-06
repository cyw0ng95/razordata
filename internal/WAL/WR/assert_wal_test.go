//go:build debug

package wr

import (
	"testing"
)

func TestWAL_Assert_SegmentBoundary_HappyPath(t *testing.T) {
	w, _ := newTestWriter(t)

	if _, err := w.Append(&WriteBatch{TxnID: 0, Recs: []LogRecord{
		{Type: RTRollback},
	}}); err != nil {
		t.Fatalf("prime: %v", err)
	}

	rec1 := LogRecord{Type: RTData, BlockID: 1, Value: []byte("a")}
	rec1Size := int64(len(encodeRecord(&rec1)))
	w.seg.writeOff = SegSize - rec1Size

	if _, err := w.Append(&WriteBatch{TxnID: 1, Recs: []LogRecord{rec1}}); err != nil {
		t.Fatalf("Append[1]: %v", err)
	}
	rec2 := LogRecord{Type: RTData, BlockID: 2, Value: []byte("b")}
	if _, err := w.Append(&WriteBatch{TxnID: 2, Recs: []LogRecord{rec2}}); err != nil {
		t.Fatalf("Append[2]: %v", err)
	}
	if w.seg.number != 1 {
		t.Errorf("expected rotation to seg 1, got seg %d", w.seg.number)
	}
}