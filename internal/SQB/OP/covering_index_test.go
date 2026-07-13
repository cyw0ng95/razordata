package OP

import (
	"context"
	"errors"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

func TestIndexScanCoveringHelperValues(t *testing.T) {
	intVal := coveringRawToValue([]byte{0, 0, 0, 0, 0, 0, 0, 1}, LX.T_INT_KW)
	if intVal.Kind != pl.KindInt || intVal.I64 != 1 {
		t.Fatalf("int decode = %v, want 1", intVal)
	}
	txtVal := coveringRawToValue([]byte("hello"), LX.T_VARCHAR)
	if txtVal.Kind != pl.KindText || txtVal.S != "hello" {
		t.Fatalf("text decode = %v, want hello", txtVal)
	}
	blVal := coveringRawToValue([]byte{1, 2, 3}, LX.T_BLOB)
	if blVal.Kind != pl.KindBlob || len(blVal.B) != 3 {
		t.Fatalf("blob decode = %v", blVal)
	}
}

func TestIndexScanCoveringHelperSplit(t *testing.T) {
	raw := []byte("\x00\x00\x00\x00\x00\x00\x00\x07alice" + "\x00" + "bob")
	v, rest, ok := coveringSplitIndexValue(raw, LX.T_INT_KW)
	if !ok || len(v) != 8 || rest[0] != 'a' {
		t.Fatalf("int split: v=%v rest=%v ok=%v", v, rest, ok)
	}
	v2, rest2, ok := coveringSplitIndexValue(rest, LX.T_TEXT)
	if !ok || string(v2) != "alice" {
		t.Fatalf("text split 1: v=%q ok=%v", v2, ok)
	}
	v3, rest3, ok := coveringSplitIndexValue(rest2, LX.T_TEXT)
	if !ok || string(v3) != "bob" || rest3 != nil {
		t.Fatalf("text split 2: v=%q rest=%v", v3, rest3)
	}
}

func TestIndexScanCoveringE2E_NoHeapGet(t *testing.T) {
	dir := t.TempDir()
	eng, err := ls.Open(dir)
	if err != nil {
		t.Fatalf("ls.Open: %v", err)
	}
	defer eng.Close()

	DT.RegisterStoreSchema("users_ci", []string{"id", "email", "name"}, "id")
	id, ok := DT.TableIDFor("users_ci")
	if !ok {
		t.Fatal("users_ci not registered")
	}

	idxStore := ls.NewIndexStore(eng, id, "idx_email")
	if err := idxStore.Insert([]byte("alice@x.com"), int64BE(1)); err != nil {
		t.Fatal(err)
	}

	scan, err := NewIndexScanWithIndex(&engineStoreAdapter{eng: eng}, id, "users_ci", "idx_email", []byte("alice@x.com"), nil)
	if err != nil {
		t.Fatalf("NewIndexScanWithIndex: %v", err)
	}
	defer scan.Close()
	scan.SetCovering([]string{"email"}, []LX.TokenType{LX.T_VARCHAR}, "id")

	row, err := scan.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	emailIdx := -1
	idIdx := -1
	for i, c := range row.Cols {
		if c == "email" {
			emailIdx = i
		}
		if c == "id" {
			idIdx = i
		}
	}
	if emailIdx < 0 || idIdx < 0 {
		t.Fatalf("schema missing email/id cols: %v", row.Cols)
	}
	if row.Data[emailIdx].Kind != pl.KindText || row.Data[emailIdx].S != "alice@x.com" {
		t.Fatalf("covering email = %v, want alice@x.com", row.Data[emailIdx])
	}
	if row.Data[idIdx].Kind != pl.KindInt || row.Data[idIdx].I64 != 1 {
		t.Fatalf("covering id = %v, want 1", row.Data[idIdx])
	}

	_, err = scan.Next(context.Background())
	if !errors.Is(err, DT.ErrNoRows) {
		t.Errorf("second Next = %v, want ErrNoRows", err)
	}
}

type engineStoreAdapter struct {
	eng *ls.Engine
}

func (a *engineStoreAdapter) Get(key []byte) ([]byte, bool, error) {
	v, err := a.eng.Get(key)
	return v, false, err
}

func (a *engineStoreAdapter) Insert(key, value []byte) error {
	return a.eng.Insert(key, value)
}

func (a *engineStoreAdapter) Delete(key []byte) error {
	return a.eng.Delete(key)
}

func (a *engineStoreAdapter) NewIterator(prefix []byte) ls.RangeIter {
	return a.eng.NewIterator(prefix)
}

func (a *engineStoreAdapter) ManualCompact() error { return nil }

func int64BE(n int64) []byte {
	b := make([]byte, 8)
	u := uint64(n)
	for i := 7; i >= 0; i-- {
		b[i] = byte(u)
		u >>= 8
	}
	return b
}
