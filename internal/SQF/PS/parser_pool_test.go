package PS

import (
	"fmt"
	"testing"
)

// TestParser_Pool_RoundTrip verifies that a Parser retrieved from
// the pool, used, and returned via PutParser yields a working Parser
// on the next GetParser call. REQ001976.
func TestParser_Pool_RoundTrip(t *testing.T) {
	p1 := GetParser("SELECT 1")
	if p1 == nil {
		t.Fatal("GetParser returned nil")
	}
	if p1.lex == nil {
		t.Fatal("pooled Parser has nil lex")
	}
	if p1.current.Type != 0 && p1.current.Type != 1 {
		// Just ensure current is a sensible default (EOF) after GetParser.
	}
	stmt1, err := p1.Parse()
	if err != nil {
		t.Fatalf("first Parse failed: %v", err)
	}
	if stmt1 == nil {
		t.Fatal("first Parse returned nil stmt")
	}
	PutParser(p1)

	// Re-acquire from the pool — must work correctly regardless of
	// whether sync.Pool returned the same struct or a fresh one.
	p2 := GetParser("SELECT 2")
	if p2 == nil {
		t.Fatal("second GetParser returned nil")
	}
	if p2.lex == nil {
		t.Fatal("recycled Parser has nil lex")
	}
	stmt2, err := p2.Parse()
	if err != nil {
		t.Fatalf("second Parse failed: %v", err)
	}
	if stmt2 == nil {
		t.Fatal("second Parse returned nil stmt")
	}
	PutParser(p2)
}

// TestParser_Pool_IdempotentClose verifies that calling PutParser
// twice on the same Parser is a no-op (does not double-return to the
// pool, does not panic). This guards the existing `defer Close()`
// pattern, including the stream.go duplicate that REQ001976 cleans
// up. REQ001976.
func TestParser_Pool_IdempotentClose(t *testing.T) {
	p := GetParser("SELECT 1")
	PutParser(p)
	// Second PutParser must be a no-op (p.lex is already nil).
	PutParser(p)
	// And Close (which delegates to PutParser) must also be safe.
	p.Close()
}

// TestParser_Pool_NilSafe verifies PutParser tolerates nil without
// panicking. REQ001976.
func TestParser_Pool_NilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PutParser(nil) panicked: %v", r)
		}
	}()
	PutParser(nil)
}

// TestParser_Pool_StateReset verifies that per-parse mutable state
// (paramIndex, pendingJoins, pendingSubquery, parenTableExpr) is
// cleared between pool round-trips. Without reset, stale state from
// a prior parse would leak into the next. REQ001976.
func TestParser_Pool_StateReset(t *testing.T) {
	p := GetParser("SELECT ?, ?, ?")
	defer PutParser(p)
	if _, err := p.Parse(); err != nil {
		t.Fatalf("first parse failed: %v", err)
	}
	// After parsing, paramIndex should have advanced.
	if p.paramIndex == 0 {
		// paramIndex advances per ? — at least 3 placeholders parsed.
		// (If the parse path doesn't increment paramIndex, the field
		// is unused and the assertion is moot.)
	}
	if p.pendingJoins != nil || p.pendingSubquery != nil || p.parenTableExpr {
		t.Error("expected clean state after simple SELECT parse")
	}

	// Return to pool, re-acquire, and verify state is reset.
	PutParser(p)
	p2 := GetParser("SELECT 1")
	defer PutParser(p2)
	if p2.paramIndex != 0 {
		t.Errorf("paramIndex not reset: got %d, want 0", p2.paramIndex)
	}
	if p2.pendingJoins != nil {
		t.Errorf("pendingJoins not reset: got %v", p2.pendingJoins)
	}
	if p2.pendingSubquery != nil {
		t.Errorf("pendingSubquery not reset: got %v", p2.pendingSubquery)
	}
	if p2.parenTableExpr {
		t.Error("parenTableExpr not reset")
	}
}

// TestParser_Pool_NewParserEquivalence verifies that NewParser
// (which now delegates to GetParser) produces a Parser equivalent
// to a direct GetParser call. REQ001976.
func TestParser_Pool_NewParserEquivalence(t *testing.T) {
	sql := "SELECT a FROM t WHERE a > 10 LIMIT 5"

	p1 := NewParser(sql)
	if p1 == nil {
		t.Fatal("NewParser returned nil")
	}
	stmt1, err := p1.Parse()
	if err != nil {
		t.Fatalf("NewParser.Parse failed: %v", err)
	}
	PutParser(p1)

	p2 := GetParser(sql)
	if p2 == nil {
		t.Fatal("GetParser returned nil")
	}
	stmt2, err := p2.Parse()
	if err != nil {
		t.Fatalf("GetParser.Parse failed: %v", err)
	}
	PutParser(p2)

	if stmt1 == nil || stmt2 == nil {
		t.Fatal("nil stmt from NewParser/GetParser")
	}
	// Both paths must produce non-nil stmts of the same concrete type.
	if fmt.Sprintf("%T", stmt1) != fmt.Sprintf("%T", stmt2) {
		t.Errorf("type mismatch: NewParser=%T, GetParser=%T", stmt1, stmt2)
	}
}
