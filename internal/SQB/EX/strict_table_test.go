package EX

import (
	"context"
	"strings"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestStrict_TypeEnforced_InsertMismatch_Error verifies REQ001369:
// STRICT table rejects INSERT when value Kind doesn't match the
// declared column affinity.
func TestStrict_TypeEnforced_InsertMismatch_Error(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE s1 (id INTEGER, name TEXT) STRICT"); err != nil {
		t.Fatal(err)
	}

	// INSERT text into INTEGER column — should fail under STRICT.
	_, err := e.Exec(ctx, "INSERT INTO s1 VALUES ('not-a-number', 'x')")
	if err == nil {
		t.Fatal("expected STRICT type mismatch error")
	}
	if !strings.Contains(err.Error(), "STRICT") {
		t.Errorf("expected STRICT-related error, got: %v", err)
	}

	// Verify row was NOT inserted.
	rows, err := e.QueryAll(ctx, "SELECT id FROM s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows after failed insert, got %d", len(rows))
	}
}

// TestStrict_TypeEnforced_NullAllowed verifies REQ001369: NULL is
// still permitted on nullable columns in STRICT mode.
func TestStrict_TypeEnforced_NullAllowed(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE s2 (id INTEGER, name TEXT) STRICT"); err != nil {
		t.Fatal(err)
	}

	if _, err := e.Exec(ctx, "INSERT INTO s2 VALUES (1, NULL)"); err != nil {
		t.Fatalf("NULL on nullable column should pass: %v", err)
	}
	rows, err := e.QueryAll(ctx, "SELECT id FROM s2")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}
}

// TestStrict_TypeEnforced_CompatibleTypes verifies REQ001369: STRICT
// permits type-promotion paths (e.g., int into REAL column).
func TestStrict_TypeEnforced_CompatibleTypes(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE s3 (id REAL, name TEXT) STRICT"); err != nil {
		t.Fatal(err)
	}

	// Int into REAL column should pass (REAL accepts INT).
	if _, err := e.Exec(ctx, "INSERT INTO s3 VALUES (42, 'hello')"); err != nil {
		t.Fatalf("int into REAL column should pass: %v", err)
	}
}

// TestStrict_NotEnabled_AllowsAnything verifies REQ001369: without
// STRICT, type mismatches are silently accepted (legacy behavior).
func TestStrict_NotEnabled_AllowsAnything(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE s4 (id INTEGER, name TEXT)"); err != nil {
		t.Fatal(err)
	}

	// Without STRICT, mismatched types are accepted.
	if _, err := e.Exec(ctx, "INSERT INTO s4 VALUES ('text-in-int', 'x')"); err != nil {
		t.Fatalf("non-STRICT should accept any type: %v", err)
	}
}

// Verify the Strict flag was wired to StoreSchema.
func TestStrict_FlagPersisted(t *testing.T) {
	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	if _, err := e.Exec(ctx, "CREATE TABLE s5 (id INTEGER) STRICT"); err != nil {
		t.Fatal(err)
	}

	DT.StoreMu.Lock()
	defer DT.StoreMu.Unlock()
	if id, ok := DT.TableIDs["s5"]; ok {
		if ss, ok := DT.StoreSchemas[id]; ok {
			if !ss.Strict {
				t.Error("expected StoreSchema.Strict=true after CREATE TABLE ... STRICT")
			}
		} else {
			t.Error("StoreSchemas[s5] not found")
		}
	} else {
		t.Error("TableIDs[s5] not found")
	}
}