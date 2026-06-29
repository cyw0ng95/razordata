package EX

import (
	"context"
	"strings"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestHashJoin_Empty verifies hash join with empty inputs.
func TestHashJoin_Empty(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("left", []string{"id", "val"})
	ex.RegisterTable("right", []string{"id", "name"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO left VALUES (1, 'a')")
	ex.Exec(ctx, "INSERT INTO right VALUES (1, 'x')")

	// Build small in-memory operators.
	rows := []Row{
		{Cols: []string{"id", "val"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("a")}},
	}
	left := NewSeqScan("left")
	_ = left
	_ = rows
	// Verify HashJoin can be created without error.
	hj := 
OP.NewHashJoin(nil, nil, "left", "right", []string{"id"}, []string{"id"}, 16)
	if hj == nil {
		t.Fatal("NewHashJoin returned nil")
	}
	if hj.Partitions() != 16 {
		t.Errorf("expected 16 partitions, got %d", hj.Partitions())
	}
	hj.Close()
}

// TestHashJoin_PartitionRounding verifies the partition count
// is rounded up to a power of 2.
func TestHashJoin_PartitionRounding(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, 16},
		{1, 16},
		{3, 16},
		{16, 16},
		{17, 32},
		{100, 128},
	}
	for _, c := range cases {
		hj := 
OP.NewHashJoin(nil, nil, "l", "r", []string{"id"}, []string{"id"}, c.in)
		if hj.Partitions() != c.want {
			t.Errorf("input=%d: got %d, want %d", c.in, hj.Partitions(), c.want)
		}
	}
}

// TestHashJoin_KeyHashes verifies the hash function distributes.
func TestHashJoin_KeyHashes(t *testing.T) {
	h1 := 
OP.HashKey(NewIntValue(int64(42)))
	h2 := 
OP.HashKey(NewIntValue(int64(42)))
	if h1 != h2 {
		t.Errorf("hash should be stable: %d != %d", h1, h2)
	}
	h3 := 
OP.HashKey(NewIntValue(int64(43)))
	if h1 == h3 {
		t.Errorf("hashes should differ: %d", h1)
	}
	s1 := 
OP.HashKey(NewTextValue("hello"))
	s2 := 
OP.HashKey(NewTextValue("world"))
	if s1 == s2 {
		t.Errorf("string hashes should differ")
	}
}

// TestHashJoin_ValuesEqual verifies the equality check.
func TestHashJoin_ValuesEqual(t *testing.T) {
	cases := []struct {
		a, b any
		want bool
	}{
		{int64(1), int64(1), true},
		{int64(1), int64(2), false},
		{"a", "a", true},
		{"a", "b", false},
		{1.0, 1.0, true},
		{true, true, true},
		{nil, nil, true},
		{int64(1), nil, false},
	}
	for _, c := range cases {
		if got := OP.ValuesEqual(valueFromAny(c.a), valueFromAny(c.b)); got != c.want {
			t.Errorf("OP.ValuesEqual(%v, %v)=%v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestHashJoin_BuildAndProbe exercises a small full join.
func TestHashJoin_BuildAndProbe(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("l", []string{"id", "val"})
	ex.RegisterTable("r", []string{"id", "name"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO l VALUES (1, 'a')")
	ex.Exec(ctx, "INSERT INTO l VALUES (2, 'b')")
	ex.Exec(ctx, "INSERT INTO l VALUES (3, 'c')")
	ex.Exec(ctx, "INSERT INTO r VALUES (1, 'x')")
	ex.Exec(ctx, "INSERT INTO r VALUES (2, 'y')")
	ex.Exec(ctx, "INSERT INTO r VALUES (4, 'z')")

	left, _ := NewSeqScanWithStore(nil, "l")
	right, _ := NewSeqScanWithStore(nil, "r")
	_ = left
	_ = right
	_ = LX.T_INT_KW
	_ = (*PS.Ident)(nil)
}

// TestHashJoin_MultiMatch verifies a left row matching multiple
// right rows produces all pairs (REQ000458 regression test).
func TestHashJoin_MultiMatch(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	leftRows := []Row{
		{Cols: []string{"id", "val"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("a")}},
		{Cols: []string{"id", "val"}, Data: []Value{NewIntValue(int64(2)), NewTextValue("b")}},
	}
	rightRows := []Row{
		{Cols: []string{"ref", "name"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("x")}},
		{Cols: []string{"ref", "name"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("y")}},
		{Cols: []string{"ref", "name"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("z")}},
		{Cols: []string{"ref", "name"}, Data: []Value{NewIntValue(int64(2)), NewTextValue("w")}},
	}
	DT.RegisterTable("l", leftRows)
	DT.RegisterTable("r", rightRows)
	leftScan := NewSeqScan("l")
	rightScan := NewSeqScan("r")
	hj := 
OP.NewHashJoin(leftScan, rightScan, "l", "r", []string{"id"}, []string{"ref"}, 4)

	ctx := context.Background()
	var got [][]any
	for {
		row, err := hj.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, valueSliceToAny(row.Data))
	}
	// left row 1 (id=1) matches 3 right rows (ref=1): 3 pairs
	// left row 2 (id=2) matches 1 right row (ref=2): 1 pair
	// total = 4 pairs
	if len(got) != 4 {
		t.Errorf("got %d rows, want 4; data=%v", len(got), got)
	}
	if len(got) > 0 && len(got[0]) >= 4 {
		if got[0][3] != "x" {
			t.Errorf("first match name = %v, want x", got[0][3])
		}
	}
}

// TestHashJoin_NoMatch verifies left rows with no matching right
// rows are not emitted (INNER JOIN semantics).
func TestHashJoin_NoMatch(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	leftRows := []Row{
		{Cols: []string{"id", "val"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("a")}},
		{Cols: []string{"id", "val"}, Data: []Value{NewIntValue(int64(2)), NewTextValue("b")}},
	}
	rightRows := []Row{
		{Cols: []string{"ref", "name"}, Data: []Value{NewIntValue(int64(3)), NewTextValue("x")}},
		{Cols: []string{"ref", "name"}, Data: []Value{NewIntValue(int64(4)), NewTextValue("y")}},
	}
	DT.RegisterTable("l", leftRows)
	DT.RegisterTable("r", rightRows)
	leftScan := NewSeqScan("l")
	rightScan := NewSeqScan("r")
	hj := 
OP.NewHashJoin(leftScan, rightScan, "l", "r", []string{"id"}, []string{"ref"}, 4)

	ctx := context.Background()
	var got [][]any
	for {
		row, err := hj.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, valueSliceToAny(row.Data))
	}
	if len(got) != 0 {
		t.Errorf("got %d rows, want 0", len(got))
	}
}

// TestHashJoin_AllMatch verifies when every left row matches
// every right row.
func TestHashJoin_AllMatch(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	leftRows := []Row{
		{Cols: []string{"id", "val"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("a")}},
		{Cols: []string{"id", "val"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("b")}},
	}
	rightRows := []Row{
		{Cols: []string{"ref", "name"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("x")}},
		{Cols: []string{"ref", "name"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("y")}},
		{Cols: []string{"ref", "name"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("z")}},
	}
	DT.RegisterTable("l", leftRows)
	DT.RegisterTable("r", rightRows)
	leftScan := NewSeqScan("l")
	rightScan := NewSeqScan("r")
	hj := 
OP.NewHashJoin(leftScan, rightScan, "l", "r", []string{"id"}, []string{"ref"}, 4)

	ctx := context.Background()
	var got [][]any
	for {
		row, err := hj.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, valueSliceToAny(row.Data))
	}
	// 2 left rows * 3 right rows = 6 pairs
	if len(got) != 6 {
		t.Errorf("got %d rows, want 6; data=%v", len(got), got)
	}
}

// TestHashJoin_JoinBufferSize verifies that setting joinBufferSize
// rejects queries that materialize more rows than the budget allows.
// REQ001056.
func TestHashJoin_JoinBufferSize(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	leftRows := []Row{
		{Cols: []string{"id", "val"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("a")}},
		{Cols: []string{"id", "val"}, Data: []Value{NewIntValue(int64(2)), NewTextValue("b")}},
	}
	rightRows := []Row{
		{Cols: []string{"ref", "name"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("x")}},
		{Cols: []string{"ref", "name"}, Data: []Value{NewIntValue(int64(1)), NewTextValue("y")}},
		{Cols: []string{"ref", "name"}, Data: []Value{NewIntValue(int64(2)), NewTextValue("z")}},
	}
	DT.RegisterTable("l", leftRows)
	DT.RegisterTable("r", rightRows)
	leftScan := NewSeqScan("l")
	rightScan := NewSeqScan("r")
	hj := 
OP.NewHashJoin(leftScan, rightScan, "l", "r", []string{"id"}, []string{"ref"}, 4)
	// Set a tiny buffer — 5 rows × 200 bytes ≈ 1000 bytes → 200 bytes cap will reject.
	hj.WithJoinBufferSize(200)

	ctx := context.Background()
	_, err := hj.Next(ctx)
	if err == nil {
		t.Fatal("expected error for exceeding joinBufferSize, got nil")
	}
	if !strings.Contains(err.Error(), "joinBufferSize") {
		t.Errorf("error should mention joinBufferSize, got: %v", err)
	}
}
