package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
)

// TestHashCrossJoin_BasicEquiJoin verifies REQ000800: HashCrossJoin
// materializes the right side into hash buckets keyed by the join
// column and probes each left row's bucket.
func TestHashCrossJoin_BasicEquiJoin(t *testing.T) {
	defer UnregisterAll()
	left := newTestSeqScan(t, "t1", []Row{
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(int64(1))}, TableName: "t1"},
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(int64(2))}, TableName: "t1"},
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(int64(3))}, TableName: "t1"},
	})
	right := newTestSeqScan(t, "t2", []Row{
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(int64(2))}, TableName: "t2"},
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(int64(3))}, TableName: "t2"},
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(int64(2))}, TableName: "t2"},
	})

	j := 
OP.NewHashCrossJoin(left, right, "t1", "t2", "a", "a")
	defer j.Close()
	ctx := context.Background()

	count := 0
	seen := map[[2]int64]int{}
	for {
		row, err := j.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		l, r := row.Data[0].ToAny().(int64), row.Data[1].ToAny().(int64)
		seen[[2]int64{l, r}]++
		count++
	}
	// Expected: t1.a=1 → no match, t1.a=2 → 2 matches, t1.a=3 → 1 match
	if count != 3 {
		t.Errorf("expected 3 matches, got %d", count)
	}
	if seen[[2]int64{1, 0}] != 0 {
		t.Errorf("t1.a=1 should have 0 matches")
	}
	if seen[[2]int64{2, 2}] != 2 {
		t.Errorf("t1.a=2 should have 2 matches, got %d", seen[[2]int64{2, 2}])
	}
	if seen[[2]int64{3, 3}] != 1 {
		t.Errorf("t1.a=3 should have 1 match, got %d", seen[[2]int64{3, 3}])
	}
}

// TestHashCrossJoin_EmptySides verifies REQ000800: empty input
// produces no rows (no panic).
func TestHashCrossJoin_EmptySides(t *testing.T) {
	defer UnregisterAll()
	left := newTestSeqScan(t, "t1", nil)
	right := newTestSeqScan(t, "t2", []Row{
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(int64(1))}, TableName: "t2"},
	})

	j := 
OP.NewHashCrossJoin(left, right, "t1", "t2", "a", "a")
	defer j.Close()
	ctx := context.Background()

	count := 0
	for {
		_, err := j.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 rows, got %d", count)
	}
}

// TestHashCrossJoin_AllMatch verifies REQ000Join builds the
// Cartesian product for full-match keys.
func TestHashCrossJoin_AllMatch(t *testing.T) {
	defer UnregisterAll()
	left := newTestSeqScan(t, "t1", []Row{
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(int64(1))}, TableName: "t1"},
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(int64(2))}, TableName: "t1"},
	})
	right := newTestSeqScan(t, "t2", []Row{
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(int64(1))}, TableName: "t2"},
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(int64(2))}, TableName: "t2"},
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(int64(1))}, TableName: "t2"},
	})

	j := 
OP.NewHashCrossJoin(left, right, "t1", "t2", "a", "a")
	defer j.Close()
	ctx := context.Background()

	count := 0
	for {
		_, err := j.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		count++
	}
	// t1.a=1 → 2 matches (right a=1,1), t1.a=2 → 1 match (right a=2)
	if count != 3 {
		t.Errorf("expected 3 matches, got %d", count)
	}
}

// TestHashCrossJoin_StringKey verifies REQ000800 supports string keys.
func TestHashCrossJoin_StringKey(t *testing.T) {
	defer UnregisterAll()
	left := newTestSeqScan(t, "t1", []Row{
		{Cols: []string{"k"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewTextValue("x")}, TableName: "t1"},
		{Cols: []string{"k"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewTextValue("y")}, TableName: "t1"},
	})
	right := newTestSeqScan(t, "t2", []Row{
		{Cols: []string{"k"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewTextValue("x")}, TableName: "t2"},
		{Cols: []string{"k"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewTextValue("x")}, TableName: "t2"},
		{Cols: []string{"k"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewTextValue("z")}, TableName: "t2"},
	})

	j := 
OP.NewHashCrossJoin(left, right, "t1", "t2", "k", "k")
	defer j.Close()
	ctx := context.Background()

	count := 0
	for {
		_, err := j.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		count++
	}
	// t1.k="x" → 2 matches, t1.k="y" → 0 matches
	if count != 2 {
		t.Errorf("expected 2 matches, got %d", count)
	}
}

// TestHashCrossJoin_NullKey verifies REQ000800: NULL keys don't match
// each other (NULL ≠ NULL in SQL semantics).
func TestHashCrossJoin_NullKey(t *testing.T) {
	defer UnregisterAll()
	left := newTestSeqScan(t, "t1", []Row{
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NullValue()}, TableName: "t1"},
	})
	right := newTestSeqScan(t, "t2", []Row{
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NullValue()}, TableName: "t2"},
	})

	j := 
OP.NewHashCrossJoin(left, right, "t1", "t2", "a", "a")
	defer j.Close()
	ctx := context.Background()

	count := 0
	for {
		_, err := j.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		count++
	}
	// NULL ≠ NULL — no matches.
	if count != 0 {
		t.Errorf("expected 0 matches (NULL≠NULL), got %d", count)
	}
}

// newTestSeqScan creates a SeqScan backed by an in-memory row slice.
// Used by HashCrossJoin tests to inject deterministic rows without
// touching the engine. Tables remain registered until the caller
// invokes UnregisterAll (typically via defer in the outer test).
// Does NOT call UnregisterAll so multiple DT.Tables can coexist.
func newTestSeqScan(t *testing.T, table string, rows []Row) *SeqScan {
	t.Helper()
	DT.TablesMu.Lock()
	DT.Tables[table] = rows
	DT.TablesMu.Unlock()
	return NewSeqScan(table)
}

// BenchmarkHashCrossJoin_SmallTables measures HashCrossJoin vs
// NestedLoopJoin on small equi-joins. Per REQ000800 expectation,
// HashCrossJoin should be ≥2× faster than NLJ when both sides are
// small enough to materialize.
func BenchmarkHashCrossJoin_SmallTables(b *testing.B) {
	_ = newBenchSeqScan("t1", 100)
	b.Run("hashcross", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			left2 := newBenchSeqScan("t1", 100)
			right2 := newBenchSeqScan("t2", 100)
			j := 
OP.NewHashCrossJoin(left2, right2, "t1", "t2", "a", "a")
			ctx := context.Background()
			for {
				_, err := j.Next(ctx)
				if err != nil {
					break
				}
			}
			j.Close()
		}
	})
	b.Run("nlj", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			left2 := newBenchSeqScan("t1", 100)
			right2 := newBenchSeqScan("t2", 100)
			j := NewNestedLoopJoin(left2, right2, "t1", "t2", nil, JoinKindInner)
			ctx := context.Background()
			for {
				_, err := j.Next(ctx)
				if err != nil {
					break
				}
			}
			j.Close()
		}
	})
}

func newBenchSeqScan(table string, n int) *SeqScan {
	rows := make([]Row, n)
	for i := 0; i < n; i++ {
		rows[i] = Row{
			Cols:      []string{"a"},
			Types:     []LX.TokenType{LX.T_INT_KW},
			Data:      []Value{NewIntValue(int64(i % 50))},
			TableName: table,
		}
	}
	DT.TablesMu.Lock()
	DT.Tables[table] = rows
	DT.TablesMu.Unlock()
	return NewSeqScan(table)
}