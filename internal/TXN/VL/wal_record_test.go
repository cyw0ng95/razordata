package VL

import (
	"testing"
)

func TestEncodeDecodeCommitRecord(t *testing.T) {
	t.Parallel()
	txnID := uint64(12345)
	commitTS := uint64(67890)
	keys := [][]byte{[]byte("key1"), []byte("key2")}

	encoded := EncodeCommitRecord(txnID, commitTS, keys)

	rec, err := DecodeCommitRecord(encoded)
	if err != nil {
		t.Fatalf("DecodeCommitRecord failed: %v", err)
	}

	if rec.Type != WALRecordCommit {
		t.Errorf("expected type %d, got %d", WALRecordCommit, rec.Type)
	}
	if rec.TxnID != txnID {
		t.Errorf("expected txnID %d, got %d", txnID, rec.TxnID)
	}
	if rec.CommitTS != commitTS {
		t.Errorf("expected commitTS %d, got %d", commitTS, rec.CommitTS)
	}
	if rec.KeyCount != uint32(len(keys)) {
		t.Errorf("expected keyCount %d, got %d", len(keys), rec.KeyCount)
	}
	if len(rec.Keys) != len(keys) {
		t.Errorf("expected %d keys, got %d", len(keys), len(rec.Keys))
	}
}

func TestEncodeCommitRecordEmptyKeys(t *testing.T) {
	t.Parallel()
	txnID := uint64(100)
	commitTS := uint64(200)

	encoded := EncodeCommitRecord(txnID, commitTS, nil)

	rec, err := DecodeCommitRecord(encoded)
	if err != nil {
		t.Fatalf("DecodeCommitRecord failed: %v", err)
	}

	if rec.KeyCount != 0 {
		t.Errorf("expected 0 keyCount, got %d", rec.KeyCount)
	}
}

func TestDecodeCommitRecordTruncated(t *testing.T) {
	t.Parallel()
	_, err := DecodeCommitRecord([]byte{1})
	if err != ErrInvalidWALRecord {
		t.Errorf("expected ErrInvalidWALRecord, got %v", err)
	}
}
