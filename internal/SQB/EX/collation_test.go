package EX

import (
	"context"
	"testing"
)

func TestRegisterCollation_CustomOrder(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	err := e.RegisterCollation("length_first", func(a, b []byte) int {
		if len(a) < len(b) {
			return -1
		}
		if len(a) > len(b) {
			return 1
		}
		for i := 0; i < len(a); i++ {
			if a[i] < b[i] {
				return -1
			}
			if a[i] > b[i] {
				return 1
			}
		}
		return 0
	})
	if err != nil {
		t.Fatal(err)
	}

	// Register a duplicate — must error.
	if err2 := e.RegisterCollation("length_first", nil); err2 == nil {
		t.Fatal("expected error on duplicate collation registration")
	}

	// Create + insert
	if _, err := e.Exec(ctx, "CREATE TABLE t (x TEXT)"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"abc", "def", "abc", "ghi"} {
		if _, err := e.Exec(ctx, "INSERT INTO t VALUES ('"+s+"')"); err != nil {
			t.Fatal(err)
		}
	}

	// Query with ORDER BY using the custom collation.
	// With "length_first" collation, strings are sorted by length first,
	// then lexicographically. So order should be: "abc" (3), "abc" (3), "def" (3), "ghi" (3).
	// All have same length, so expected ASCII order: abc, abc, def, ghi.
	rows, err := e.QueryAll(ctx, "SELECT x FROM t ORDER BY x COLLATE length_first")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(rows))
	}
	// Verify the order — with "reverse" collation applied to sorting
	expected := []string{"abc", "abc", "def", "ghi"}
	for i, r := range rows {
		if r.Data[0].S != expected[i] {
			t.Errorf("row %d: expected %q, got %q", i, expected[i], r.Data[0].S)
		}
	}
}

func TestRegisterCollation_CollateIndexOrderBy(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	// Register a nocase-like collation.
	err := e.RegisterCollation("nocase", func(a, b []byte) int {
		ua := toUpper(a)
		ub := toUpper(b)
		for i := 0; i < len(ua) && i < len(ub); i++ {
			if ua[i] < ub[i] {
				return -1
			}
			if ua[i] > ub[i] {
				return 1
			}
		}
		if len(ua) < len(ub) {
			return -1
		}
		if len(ua) > len(ub) {
			return 1
		}
		return 0
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.Exec(ctx, "CREATE TABLE t2 (x TEXT)"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"BANANA", "apple", "Cherry", "Apple"} {
		if _, err := e.Exec(ctx, "INSERT INTO t2 VALUES ('"+s+"')"); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := e.QueryAll(ctx, "SELECT x FROM t2 ORDER BY x COLLATE nocase")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(rows))
	}
	// Case-insensitive order: apple, Apple, BANANA, Cherry
	expected := []string{"apple", "Apple", "BANANA", "Cherry"}
	for i, r := range rows {
		if r.Data[0].S != expected[i] {
			t.Errorf("row %d: expected %q, got %q", i, expected[i], r.Data[0].S)
		}
	}
}

func TestRegisterCollation_UnregisteredCollation_FallsBack(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE t3 (x TEXT)"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"z", "a", "m"} {
		if _, err := e.Exec(ctx, "INSERT INTO t3 VALUES ('"+s+"')"); err != nil {
			t.Fatal(err)
		}
	}

	// Use COLLATE with an unregistered collation — should fall back to default order.
	rows, err := e.QueryAll(ctx, "SELECT x FROM t3 ORDER BY x COLLATE nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	expected := []string{"a", "m", "z"}
	for i, r := range rows {
		if r.Data[0].S != expected[i] {
			t.Errorf("row %d: expected %q, got %q", i, expected[i], r.Data[0].S)
		}
	}
}

func TestRegisterCollation_DescOrder(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	err := e.RegisterCollation("nocase", func(a, b []byte) int {
		ua := toUpper(a)
		ub := toUpper(b)
		for i := 0; i < len(ua) && i < len(ub); i++ {
			if ua[i] < ub[i] {
				return -1
			}
			if ua[i] > ub[i] {
				return 1
			}
		}
		if len(ua) < len(ub) {
			return -1
		}
		if len(ua) > len(ub) {
			return 1
		}
		return 0
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.Exec(ctx, "CREATE TABLE t4 (x TEXT)"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"a", "B", "c", "D"} {
		if _, err := e.Exec(ctx, "INSERT INTO t4 VALUES ('"+s+"')"); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := e.QueryAll(ctx, "SELECT x FROM t4 ORDER BY x DESC COLLATE nocase")
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"D", "c", "B", "a"}
	for i, r := range rows {
		if r.Data[0].S != expected[i] {
			t.Errorf("row %d: expected %q, got %q", i, expected[i], r.Data[0].S)
		}
	}
}

func toUpper(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			out[i] = c - 32
		} else {
			out[i] = c
		}
	}
	return out
}
