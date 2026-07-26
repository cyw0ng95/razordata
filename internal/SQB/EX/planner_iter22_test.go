package EX

import (
	"context"
	"fmt"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestPlanner_IndexedColumnEq(t *testing.T) {
	// Build a SELECT WHERE id = 5
	e := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "id"},
		Right: &PS.NumberLiteral{Val: 5},
	}
	col, val, ok := indexedColumnEq(e)
	if !ok {
		t.Fatal("expected ok")
	}
	if col != "id" {
		t.Errorf("col = %q, want id", col)
	}
	if len(val) != 8 {
		t.Errorf("val len = %d, want 8 (int64 BE)", len(val))
	}
}

func TestPlanner_IndexedColumnEq_String(t *testing.T) {
	e := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "email"},
		Right: &PS.StringLiteral{Val: "alice@x.com"},
	}
	col, val, ok := indexedColumnEq(e)
	if !ok {
		t.Fatal("expected ok")
	}
	if col != "email" {
		t.Errorf("col = %q, want email", col)
	}
	if string(val) != "alice@x.com" {
		t.Errorf("val = %q, want alice@x.com", val)
	}
}

func TestPlanner_IndexedColumnEq_NotEquality(t *testing.T) {
	e := &PS.BinaryExpr{
		Op:    LX.T_GT,
		Left:  &PS.Ident{Name: "id"},
		Right: &PS.NumberLiteral{Val: 5},
	}
	_, _, ok := indexedColumnEq(e)
	if ok {
		t.Error("GT should not be recognized as equality")
	}
}

func TestPlanner_IndexedColumnEq_NotLiteral(t *testing.T) {
	e := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.Ident{Name: "b"},
	}
	_, _, ok := indexedColumnEq(e)
	if ok {
		t.Error("col = col should not be recognized")
	}
}

func TestPlanner_IndexedColumnEq_Reversed(t *testing.T) {
	e := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.NumberLiteral{Val: 5},
		Right: &PS.Ident{Name: "id"},
	}
	col, val, ok := indexedColumnEq(e)
	if !ok {
		t.Fatal("expected ok for reversed")
	}
	if col != "id" {
		t.Errorf("col = %q, want id", col)
	}
	if len(val) != 8 {
		t.Errorf("val len = %d, want 8", len(val))
	}
}

func TestPlanner_PlanSelect_PrefersIndexSeek(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("users", []string{"id", "email"}, "id")

	id, _ := DT.TableIDFor("users")

	// Manually populate the secondary index
	idxStore := ls.NewIndexStore(eng, id, "idx_email")
	for _, r := range []struct {
		id    int64
		email string
	}{
		{1, "alice@x.com"},
		{2, "bob@x.com"},
		{3, "carol@x.com"},
	} {
		row := fmt.Sprintf(`INSERT INTO users VALUES (%d, '%s')`, r.id, r.email)
		if _, err := ex.Exec(context.Background(), row); err != nil {
			t.Fatal(err)
		}
		_ = idxStore.Insert([]byte(r.email), int64ToBytes(r.id))
	}

	// Register the index with the planner
	ex.RegisterIndex("users", "idx_email", []string{"email"})

	// Query with equality on indexed column — should use real seek
	rows, err := ex.QueryAll(context.Background(),
		"SELECT id FROM users WHERE email = 'bob@x.com'")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if !rows[0].Data[0].Equal(NewIntValue(int64(2))) {
		t.Errorf("id = %v, want 2", rows[0].Data[0])
	}
}

func TestEncodeIndexValue(t *testing.T) {
	tests := []struct {
		name       string
		expr       PS.Expr
		wantLen    int
		wantSuffix byte
	}{
		{"int-42", &PS.NumberLiteral{Val: 42}, 8, 42},
		{"int-neg", &PS.NumberLiteral{Val: -1}, 8, 0xFF},
		{"str", &PS.StringLiteral{Val: "hello"}, 5, 'o'},
		{"bool-true", &PS.BoolLiteral{Val: true}, 1, 1},
		{"bool-false", &PS.BoolLiteral{Val: false}, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := encodeIndexValue(tt.expr)
			if !ok {
				t.Fatal("encodeIndexValue: not ok")
			}
			if len(got) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(got), tt.wantLen)
			}
			if got[len(got)-1] != tt.wantSuffix {
				t.Errorf("last byte = %d, want %d", got[len(got)-1], tt.wantSuffix)
			}
		})
	}
}
