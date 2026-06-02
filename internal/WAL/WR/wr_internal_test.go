package wr

import (
	"testing"
)

func TestDecodeRecord_InvalidOffset(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03}

	_, _, err := DecodeRecord(data, -1)
	if err != ErrTruncatedRecord {
		t.Fatalf("expected ErrTruncatedRecord for offset=-1, got %v", err)
	}

	_, _, err = DecodeRecord(data, len(data))
	if err != ErrTruncatedRecord {
		t.Fatalf("expected ErrTruncatedRecord for offset=len(data), got %v", err)
	}
}

func TestDecodeRecord_TruncatedLength(t *testing.T) {
	data := []byte{0xFF, 0xFF, 0xFF}

	_, _, err := DecodeRecord(data, 0)
	if err != ErrTruncatedRecord {
		t.Fatalf("expected ErrTruncatedRecord for truncated length, got %v", err)
	}
}

func TestDecodeRecord_UnknownRecord(t *testing.T) {
	rec := &LogRecord{
		Type:  RTData,
		TxnID: 1,
		Key:   []byte("test"),
		Value: []byte("value"),
	}

	buf := encodeRecord(rec)
	if buf == nil {
		t.Fatal("encodeRecord returned nil")
	}

	decoded, _, err := DecodeRecord(buf, 0)
	if err != nil {
		t.Fatalf("DecodeRecord failed: %v", err)
	}

	if decoded.Type != RTData {
		t.Fatalf("expected RTData, got %v", decoded.Type)
	}
}

func TestDecodeRecord_TypeRTData(t *testing.T) {
	rec := &LogRecord{
		Type:  RTData,
		TxnID: 1,
		Key:   []byte("key"),
		Value: []byte("val"),
	}

	buf := encodeRecord(rec)
	decoded, _, err := DecodeRecord(buf, 0)
	if err != nil {
		t.Fatalf("DecodeRecord failed: %v", err)
	}

	if decoded.Type != RTData {
		t.Fatalf("expected RTData, got %v", decoded.Type)
	}
}

func TestDecodeRecord_TypeRTCommit(t *testing.T) {
	rec := &LogRecord{
		Type:  RTCommit,
		TxnID: 1,
		Key:   []byte("key"),
		Value: []byte("val"),
	}

	buf := encodeRecord(rec)
	decoded, _, err := DecodeRecord(buf, 0)
	if err != nil {
		t.Fatalf("DecodeRecord failed: %v", err)
	}

	if decoded.Type != RTCommit {
		t.Fatalf("expected RTCommit, got %v", decoded.Type)
	}
}

func TestDecodeRecord_TypeRTRollback(t *testing.T) {
	rec := &LogRecord{
		Type:  RTRollback,
		TxnID: 1,
	}

	buf := encodeRecord(rec)
	decoded, _, err := DecodeRecord(buf, 0)
	if err != nil {
		t.Fatalf("DecodeRecord failed: %v", err)
	}

	if decoded.Type != RTRollback {
		t.Fatalf("expected RTRollback, got %v", decoded.Type)
	}
}

func TestDecodeRecord_TypeRTCheckpoint(t *testing.T) {
	rec := &LogRecord{
		Type:  RTCheckpoint,
		TxnID: 1,
	}

	buf := encodeRecord(rec)
	decoded, _, err := DecodeRecord(buf, 0)
	if err != nil {
		t.Fatalf("DecodeRecord failed: %v", err)
	}

	if decoded.Type != RTCheckpoint {
		t.Fatalf("expected RTCheckpoint, got %v", decoded.Type)
	}
}

func TestDecodeRecord_LargeTxnID(t *testing.T) {
	rec := &LogRecord{
		Type:  RTData,
		TxnID: 1 << 62,
		Key:   []byte("key"),
		Value: []byte("val"),
	}

	buf := encodeRecord(rec)
	decoded, _, err := DecodeRecord(buf, 0)
	if err != nil {
		t.Fatalf("DecodeRecord failed: %v", err)
	}

	if decoded.TxnID != 1<<62 {
		t.Fatalf("expected TxnID=%d, got %d", 1<<62, decoded.TxnID)
	}
}

func TestDecodeRecord_EmptyPayload(t *testing.T) {
	rec := &LogRecord{
		Type:  RTCommit,
		TxnID: 1,
	}

	buf := encodeRecord(rec)
	decoded, _, err := DecodeRecord(buf, 0)
	if err != nil {
		t.Fatalf("DecodeRecord failed: %v", err)
	}

	if decoded.Type != RTCommit {
		t.Fatalf("expected RTCommit, got %v", decoded.Type)
	}
}



func TestRecordType_ByteValues(t *testing.T) {
	if RTData != 0 {
		t.Fatalf("expected RTData=0, got %d", RTData)
	}
	if RTCommit != 1 {
		t.Fatalf("expected RTCommit=1, got %d", RTCommit)
	}
	if RTRollback != 2 {
		t.Fatalf("expected RTRollback=2, got %d", RTRollback)
	}
	if RTCheckpoint != 3 {
		t.Fatalf("expected RTCheckpoint=3, got %d", RTCheckpoint)
	}
	if RTMerge != 4 {
		t.Fatalf("expected RTMerge=4, got %d", RTMerge)
	}
}

func TestWriteBatch_Recs(t *testing.T) {
	wb := &WriteBatch{
		Recs: []LogRecord{
			{Type: RTData, TxnID: 1, Key: []byte("k1"), Value: []byte("v1")},
			{Type: RTData, TxnID: 1, Key: []byte("k2"), Value: []byte("v2")},
		},
		TxnID: 1,
	}

	if len(wb.Recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(wb.Recs))
	}
}

func TestWriteBatch_Empty(t *testing.T) {
	wb := &WriteBatch{
		Recs:  []LogRecord{},
		TxnID: 1,
	}

	if len(wb.Recs) != 0 {
		t.Fatalf("expected 0 records, got %d", len(wb.Recs))
	}
}

func TestWriteBatch_SetTxnID(t *testing.T) {
	wb := &WriteBatch{
		Recs: []LogRecord{
			{Type: RTData, TxnID: 0, Key: []byte("k1"), Value: []byte("v1")},
		},
		TxnID: 42,
	}

	for i := range wb.Recs {
		wb.Recs[i].TxnID = wb.TxnID
	}

	if wb.Recs[0].TxnID != 42 {
		t.Fatalf("expected TxnID=42, got %d", wb.Recs[0].TxnID)
	}
}

func TestLogRecord_Fields(t *testing.T) {
	rec := LogRecord{
		Type:    RTData,
		TxnID:   100,
		BlockID:  1,
		Key:     []byte("key"),
		Value:   []byte("value"),
	}

	if rec.Type != RTData {
		t.Fatalf("expected RTData, got %v", rec.Type)
	}
	if rec.TxnID != 100 {
		t.Fatalf("expected TxnID=100, got %d", rec.TxnID)
	}
	if rec.BlockID != 1 {
		t.Fatalf("expected BlockID=1, got %d", rec.BlockID)
	}
	if string(rec.Key) != "key" {
		t.Fatalf("expected Key='key', got %s", string(rec.Key))
	}
	if string(rec.Value) != "value" {
		t.Fatalf("expected Value='value', got %s", string(rec.Value))
	}
}
