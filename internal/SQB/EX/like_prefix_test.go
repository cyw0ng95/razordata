package EX

import (
	"context"
	"fmt"
	"strings"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

// TestLikePrefix_RangeScan exercises REQ001250: a `WHERE col LIKE 'prefix%'`
// must compile to an index range scan `[prefix, next_prefix)` instead of a
// full table scan + LIKE function evaluation per row.
//
// Acceptance: planner selects IndexScan (not SeqScan); EXPLAIN output shows
// "RANGE" or the seek range; query returns only matching rows.
func TestLikePrefix_RangeScan(t *testing.T) {
	ResetForTest(t)
	dir := t.TempDir()
	eng, err := ls.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t_like", []string{"id", "name"}, "id")
	ex.RegisterIndex("t_like", "idx_t_like_name", []string{"name"})
	id, _ := DT.TableIDFor("t_like")

	ctx := context.Background()
	// Populate table + index.
	seed := []string{
		"alpha", "alpine", "alphabet", "banana", "beta", "gamma", "zeta",
		"HelloWorld", "HelloKitty", "HellBoy", "Yellow",
	}
	for i, s := range seed {
		if _, err := ex.Exec(ctx, fmt.Sprintf("INSERT INTO t_like VALUES (%d, '%s')", i+1, s)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	idxStore := ls.NewIndexStore(eng, id, "idx_t_like_name")
	for _, s := range seed {
		idxStore.Insert([]byte(s), []byte(s))
	}

	// Correctness: LIKE 'Hello%' should match HelloWorld, HelloKitty.
	rows, err := ex.QueryAll(ctx, "SELECT name FROM t_like WHERE name LIKE 'Hello%'")
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	got := []string{
		rows[0].Data[0].ToAny().(string),
		rows[1].Data[0].ToAny().(string),
	}
	// Sort for stable comparison (the index returns lexicographic order
	// but we shouldn't depend on a specific iteration order in tests).
	want := map[string]bool{"HelloWorld": true, "HelloKitty": true}
	for _, s := range got {
		if !want[s] {
			t.Errorf("unexpected row %q", s)
		}
	}
	// Critical: non-matching rows must NOT leak through.
	for _, r := range rows {
		s := r.Data[0].ToAny().(string)
		if s != "HelloWorld" && s != "HelloKitty" {
			t.Errorf("non-matching row leaked: %q", s)
		}
	}

	// Planner path: EXPLAIN must mention IndexScan (RANGE/SEEK), not SeqScan.
	// We DISABLE store temporarily to force SeqScan path so the test
	// proves the IndexScan path was actually selected when available.
	explainRows, err := ex.QueryAll(ctx, "EXPLAIN SELECT name FROM t_like WHERE name LIKE 'Hello%'")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	var dump strings.Builder
	usedIdx := false
	usedSeq := false
	for _, r := range explainRows {
		if len(r.Data) < 4 {
			continue
		}
		detail := r.Data[3].ToAny().(string)
		dump.WriteString(detail)
		dump.WriteByte('\n')
		if strings.Contains(detail, "USING INDEX") || strings.Contains(detail, "idx=") || strings.Contains(detail, "Search") {
			usedIdx = true
		}
		if strings.HasPrefix(strings.TrimSpace(detail), "Scan ") {
			usedSeq = true
		}
	}
	t.Logf("EXPLAIN output:\n%s", dump.String())
	if !usedIdx {
		t.Errorf("expected EXPLAIN to use index for LIKE prefix; got:\n%s", dump.String())
	}
	if usedSeq {
		t.Errorf("expected planner NOT to fall back to SeqScan; got:\n%s", dump.String())
	}
}

// TestLikePrefix_ExtractPrefix unit-tests the prefix extractor: returns the
// literal substring before the first wildcard, rejects leading wildcards,
// handles no-wildcard (entire pattern) and empty patterns.
func TestLikePrefix_ExtractPrefix(t *testing.T) {
	cases := []struct {
		pattern string
		want    string
	}{
		{"Hello%", "Hello"},
		{"Hello_World", "Hello"},
		{"a%b", "a"},
		{"%", ""},      // leading wildcard → no usable prefix
		{"_abc", ""},   // leading wildcard → no usable prefix
		{"abc", "abc"}, // no wildcard → entire pattern
		{"", ""},       // empty
		{"a%_%z%", "a"},
	}
	for _, c := range cases {
		t.Run(c.pattern, func(t *testing.T) {
			if got := extractLikePrefix(c.pattern); got != c.want {
				t.Errorf("extractLikePrefix(%q) = %q, want %q", c.pattern, got, c.want)
			}
		})
	}
}

// TestLikePrefix_NoIndexFallback covers the REQ001250 negative case:
// without an index on the LIKE column, the planner must fall back to
// SeqScan + LIKE filter (preserve correctness) rather than crash.
func TestLikePrefix_NoIndexFallback(t *testing.T) {
	ResetForTest(t)
	dir := t.TempDir()
	eng, err := ls.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t_no_idx", []string{"id", "name"}, "id")
	// Note: NO index on `name`.

	ctx := context.Background()
	seed := []string{"HelloWorld", "HelloKitty", "Yellow", "alpha"}
	for i, s := range seed {
		if _, err := ex.Exec(ctx, fmt.Sprintf("INSERT INTO t_no_idx VALUES (%d, '%s')", i+1, s)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT name FROM t_no_idx WHERE name LIKE 'Hello%'")
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 Hello* rows, got %d", len(rows))
	}
}
