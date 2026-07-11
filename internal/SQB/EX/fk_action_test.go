package EX

import (
	"errors"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	ap "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// TestFK_OnDelete_Cascade verifies REQ001308: CASCADE on parent DELETE
// deletes matching child rows.
func TestFK_OnDelete_Cascade(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	parent := &DT.StoreSchema{Cols: []string{"id"}, Pk: "id"}
	child := &DT.StoreSchema{
		Cols: []string{"id", "pid"},
		Pk:   "id",
		ForeignKeys: []DT.ForeignKeyConstraint{
			{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
		},
	}
	DT.StoreSchemas[1] = parent
	DT.StoreSchemas[2] = child
	DT.TableIDs["p"] = 1
	DT.TableIDs["c"] = 2

	DT.Tables["p"] = []DT.Row{
		{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(1)}},
		{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(2)}},
	}
	DT.Tables["c"] = []DT.Row{
		{Cols: []string{"id", "pid"}, Data: []DT.Value{DT.NewIntValue(100), DT.NewIntValue(1)}},
		{Cols: []string{"id", "pid"}, Data: []DT.Value{DT.NewIntValue(200), DT.NewIntValue(2)}},
		{Cols: []string{"id", "pid"}, Data: []DT.Value{DT.NewIntValue(300), DT.NewIntValue(1)}},
	}

	err := UT.ValidateForeignKeyDeleteInMemory("p", []any{int64(1)}, parent)
	if err != nil {
		t.Fatalf("expected CASCADE to succeed, got: %v", err)
	}
	if len(DT.Tables["c"]) != 1 {
		t.Fatalf("expected 1 child row after cascade, got %d", len(DT.Tables["c"]))
	}
	if DT.Tables["c"][0].Data[1].I64 != 2 {
		t.Fatalf("expected remaining child pid=2, got %v", DT.Tables["c"][0].Data[1].I64)
	}
}

// TestFK_OnDelete_SetNull verifies REQ001308: SET NULL on parent DELETE
// sets child FK columns to NULL.
func TestFK_OnDelete_SetNull(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	parent := &DT.StoreSchema{Cols: []string{"id"}, Pk: "id"}
	child := &DT.StoreSchema{
		Cols: []string{"id", "pid"},
		Pk:   "id",
		ForeignKeys: []DT.ForeignKeyConstraint{
			{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "SET NULL"},
		},
	}
	DT.StoreSchemas[1] = parent
	DT.StoreSchemas[2] = child
	DT.TableIDs["p"] = 1
	DT.TableIDs["c"] = 2

	DT.Tables["p"] = []DT.Row{
		{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(1)}},
	}
	DT.Tables["c"] = []DT.Row{
		{Cols: []string{"id", "pid"}, Data: []DT.Value{DT.NewIntValue(100), DT.NewIntValue(1)}},
	}

	err := UT.ValidateForeignKeyDeleteInMemory("p", []any{int64(1)}, parent)
	if err != nil {
		t.Fatalf("expected SET NULL to succeed, got: %v", err)
	}
	if len(DT.Tables["c"]) != 1 {
		t.Fatalf("expected child row to remain, got %d rows", len(DT.Tables["c"]))
	}
	if DT.Tables["c"][0].Data[1].Kind != DT.KindNull {
		t.Fatalf("expected child pid=NULL, got %v", DT.Tables["c"][0].Data[1])
	}
}

// TestFK_OnDelete_SetDefault verifies REQ001308: SET DEFAULT sets child
// FK columns to NULL (simplified v1).
func TestFK_OnDelete_SetDefault(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	parent := &DT.StoreSchema{Cols: []string{"id"}, Pk: "id"}
	child := &DT.StoreSchema{
		Cols: []string{"id", "pid"},
		Pk:   "id",
		ForeignKeys: []DT.ForeignKeyConstraint{
			{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "SET DEFAULT"},
		},
	}
	DT.StoreSchemas[1] = parent
	DT.StoreSchemas[2] = child
	DT.TableIDs["p"] = 1
	DT.TableIDs["c"] = 2

	DT.Tables["p"] = []DT.Row{
		{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(1)}},
	}
	DT.Tables["c"] = []DT.Row{
		{Cols: []string{"id", "pid"}, Data: []DT.Value{DT.NewIntValue(100), DT.NewIntValue(1)}},
	}

	err := UT.ValidateForeignKeyDeleteInMemory("p", []any{int64(1)}, parent)
	if err != nil {
		t.Fatalf("expected SET DEFAULT to succeed, got: %v", err)
	}
	if len(DT.Tables["c"]) != 1 {
		t.Fatalf("expected child row to remain, got %d rows", len(DT.Tables["c"]))
	}
	if DT.Tables["c"][0].Data[1].Kind != DT.KindNull {
		t.Fatalf("expected child pid=NULL, got %v", DT.Tables["c"][0].Data[1])
	}
}

// TestFK_OnDelete_Restrict verifies REQ001308: RESTRICT blocks DELETE
// when child rows exist.
func TestFK_OnDelete_Restrict(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	parent := &DT.StoreSchema{Cols: []string{"id"}, Pk: "id"}
	child := &DT.StoreSchema{
		Cols: []string{"id", "pid"},
		Pk:   "id",
		ForeignKeys: []DT.ForeignKeyConstraint{
			{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "RESTRICT"},
		},
	}
	DT.StoreSchemas[1] = parent
	DT.StoreSchemas[2] = child
	DT.TableIDs["p"] = 1
	DT.TableIDs["c"] = 2

	DT.Tables["p"] = []DT.Row{{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(1)}}}
	DT.Tables["c"] = []DT.Row{{Cols: []string{"id", "pid"}, Data: []DT.Value{DT.NewIntValue(100), DT.NewIntValue(1)}}}

	err := UT.ValidateForeignKeyDeleteInMemory("p", []any{int64(1)}, parent)
	if err == nil {
		t.Fatal("expected FK violation, got nil")
	}
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("err = %v, want wrap of ErrConstraint", err)
	}
}

// TestFK_OnDelete_NoAction verifies REQ001308: NO ACTION blocks DELETE
// when child rows exist.
func TestFK_OnDelete_NoAction(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	parent := &DT.StoreSchema{Cols: []string{"id"}, Pk: "id"}
	child := &DT.StoreSchema{
		Cols: []string{"id", "pid"},
		Pk:   "id",
		ForeignKeys: []DT.ForeignKeyConstraint{
			{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "NO ACTION"},
		},
	}
	DT.StoreSchemas[1] = parent
	DT.StoreSchemas[2] = child
	DT.TableIDs["p"] = 1
	DT.TableIDs["c"] = 2

	DT.Tables["p"] = []DT.Row{{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(1)}}}
	DT.Tables["c"] = []DT.Row{{Cols: []string{"id", "pid"}, Data: []DT.Value{DT.NewIntValue(100), DT.NewIntValue(1)}}}

	err := UT.ValidateForeignKeyDeleteInMemory("p", []any{int64(1)}, parent)
	if err == nil {
		t.Fatal("expected FK violation, got nil")
	}
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("err = %v, want wrap of ErrConstraint", err)
	}
}

// TestFK_OnDelete_CascadeRecursive verifies REQ001308: recursive CASCADE
// through multiple levels.
func TestFK_OnDelete_CascadeRecursive(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	gp := &DT.StoreSchema{Cols: []string{"id"}, Pk: "id"}
	p := &DT.StoreSchema{
		Cols: []string{"id", "gid"},
		Pk:   "id",
		ForeignKeys: []DT.ForeignKeyConstraint{
			{Columns: []string{"gid"}, RefTable: "gp", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
		},
	}
	c := &DT.StoreSchema{
		Cols: []string{"id", "pid"},
		Pk:   "id",
		ForeignKeys: []DT.ForeignKeyConstraint{
			{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
		},
	}
	DT.StoreSchemas[1] = gp
	DT.StoreSchemas[2] = p
	DT.StoreSchemas[3] = c
	DT.TableIDs["gp"] = 1
	DT.TableIDs["p"] = 2
	DT.TableIDs["c"] = 3

	DT.Tables["gp"] = []DT.Row{{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(1)}}}
	DT.Tables["p"] = []DT.Row{{Cols: []string{"id", "gid"}, Data: []DT.Value{DT.NewIntValue(10), DT.NewIntValue(1)}}}
	DT.Tables["c"] = []DT.Row{{Cols: []string{"id", "pid"}, Data: []DT.Value{DT.NewIntValue(100), DT.NewIntValue(10)}}}

	err := UT.ValidateForeignKeyDeleteInMemory("gp", []any{int64(1)}, gp)
	if err != nil {
		t.Fatalf("expected recursive CASCADE to succeed, got: %v", err)
	}
	if len(DT.Tables["gp"]) != 1 {
		t.Errorf("expected 1 gp rows (caller deletes parent), got %d", len(DT.Tables["gp"]))
	}
	if len(DT.Tables["p"]) != 0 {
		t.Errorf("expected 0 parent rows, got %d", len(DT.Tables["p"]))
	}
	if len(DT.Tables["c"]) != 0 {
		t.Errorf("expected 0 child rows, got %d", len(DT.Tables["c"]))
	}
}

// TestFK_OnUpdate_Cascade verifies REQ001309: CASCADE on parent UPDATE.
func TestFK_OnUpdate_Cascade(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	parent := &DT.StoreSchema{Cols: []string{"id"}, Pk: "id"}
	child := &DT.StoreSchema{
		Cols: []string{"id", "pid"},
		Pk:   "id",
		ForeignKeys: []DT.ForeignKeyConstraint{
			{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "RESTRICT", OnUpdate: "CASCADE"},
		},
	}
	DT.StoreSchemas[1] = parent
	DT.StoreSchemas[2] = child
	DT.TableIDs["p"] = 1
	DT.TableIDs["c"] = 2

	DT.Tables["p"] = []DT.Row{
		{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(1)}},
	}
	DT.Tables["c"] = []DT.Row{
		{Cols: []string{"id", "pid"}, Data: []DT.Value{DT.NewIntValue(100), DT.NewIntValue(1)}},
		{Cols: []string{"id", "pid"}, Data: []DT.Value{DT.NewIntValue(200), DT.NewIntValue(1)}},
	}

	err := UT.ApplyForeignKeyOnUpdateInMemory("p",
		[]any{int64(1)},
		[]any{int64(99)},
	)
	if err != nil {
		t.Fatalf("expected CASCADE to succeed, got: %v", err)
	}
	if DT.Tables["c"][0].Data[1].I64 != 99 {
		t.Errorf("child 0 pid: got %d, want 99", DT.Tables["c"][0].Data[1].I64)
	}
	if DT.Tables["c"][1].Data[1].I64 != 99 {
		t.Errorf("child 1 pid: got %d, want 99", DT.Tables["c"][1].Data[1].I64)
	}
}

// TestFK_OnUpdate_SetNull verifies REQ001309: SET NULL on parent UPDATE.
func TestFK_OnUpdate_SetNull(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	parent := &DT.StoreSchema{Cols: []string{"id"}, Pk: "id"}
	child := &DT.StoreSchema{
		Cols: []string{"id", "pid"},
		Pk:   "id",
		ForeignKeys: []DT.ForeignKeyConstraint{
			{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "RESTRICT", OnUpdate: "SET NULL"},
		},
	}
	DT.StoreSchemas[1] = parent
	DT.StoreSchemas[2] = child
	DT.TableIDs["p"] = 1
	DT.TableIDs["c"] = 2

	DT.Tables["p"] = []DT.Row{{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(1)}}}
	DT.Tables["c"] = []DT.Row{{Cols: []string{"id", "pid"}, Data: []DT.Value{DT.NewIntValue(100), DT.NewIntValue(1)}}}

	err := UT.ApplyForeignKeyOnUpdateInMemory("p",
		[]any{int64(1)},
		[]any{int64(99)},
	)
	if err != nil {
		t.Fatalf("expected SET NULL to succeed, got: %v", err)
	}
	if DT.Tables["c"][0].Data[1].Kind != DT.KindNull {
		t.Fatalf("expected child pid=NULL, got %v", DT.Tables["c"][0].Data[1])
	}
}

// TestFK_OnUpdate_Restrict verifies REQ001309: RESTRICT blocks parent UPDATE.
func TestFK_OnUpdate_Restrict(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	parent := &DT.StoreSchema{Cols: []string{"id"}, Pk: "id"}
	child := &DT.StoreSchema{
		Cols: []string{"id", "pid"},
		Pk:   "id",
		ForeignKeys: []DT.ForeignKeyConstraint{
			{Columns: []string{"pid"}, RefTable: "p", RefColumns: []string{"id"}, OnDelete: "RESTRICT", OnUpdate: "RESTRICT"},
		},
	}
	DT.StoreSchemas[1] = parent
	DT.StoreSchemas[2] = child
	DT.TableIDs["p"] = 1
	DT.TableIDs["c"] = 2

	DT.Tables["p"] = []DT.Row{{Cols: []string{"id"}, Data: []DT.Value{DT.NewIntValue(1)}}}
	DT.Tables["c"] = []DT.Row{{Cols: []string{"id", "pid"}, Data: []DT.Value{DT.NewIntValue(100), DT.NewIntValue(1)}}}

	err := UT.ApplyForeignKeyOnUpdateInMemory("p",
		[]any{int64(1)},
		[]any{int64(99)},
	)
	if err == nil {
		t.Fatal("expected FK violation, got nil")
	}
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("err = %v, want wrap of ErrConstraint", err)
	}
}

// TestGenerated_Virtual_FKTarget verifies REQ001340: VIRTUAL generated
// column as FK target.
func TestGenerated_Virtual_FKTarget(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	// Parent table with VIRTUAL column v = a + b.
	// Build the expression manually: BinaryExpr{Op: PLUS, Left: Ident("a"), Right: Ident("b")}
	parent := &DT.StoreSchema{
		Cols: []string{"a", "b", "v"},
		Pk:   "a",
		Generated: []PS.Expr{nil, nil, &PS.BinaryExpr{
			Op:    LX.T_PLUS,
			Left:  &PS.Ident{Name: "a"},
			Right: &PS.Ident{Name: "b"},
		}},
	}
	child := &DT.StoreSchema{
		Cols: []string{"id", "ref_v"},
		Pk:   "id",
		ForeignKeys: []DT.ForeignKeyConstraint{
			{Columns: []string{"ref_v"}, RefTable: "p", RefColumns: []string{"v"}, OnDelete: "RESTRICT"},
		},
	}
	DT.StoreSchemas[1] = parent
	DT.StoreSchemas[2] = child
	DT.TableIDs["p"] = 1
	DT.TableIDs["c"] = 2

	// Parent row: a=1, b=2 => v=3 (VIRTUAL, stored as nil).
	DT.Tables["p"] = []DT.Row{
		{Cols: []string{"a", "b", "v"}, Data: []DT.Value{DT.NewIntValue(1), DT.NewIntValue(2), DT.NullValue()}},
	}

	// Child row referencing v=3 should succeed (virtual eval finds it).
	err := UT.ValidateForeignKeyUpdateInMemory(child,
		[]any{int64(100), int64(0)},
		[]any{int64(100), int64(3)},
	)
	if err != nil {
		t.Fatalf("expected FK to VIRTUAL col to succeed, got: %v", err)
	}

	// Child row referencing v=99 should fail (no parent has this value).
	err = UT.ValidateForeignKeyUpdateInMemory(child,
		[]any{int64(100), int64(0)},
		[]any{int64(100), int64(99)},
	)
	if err == nil {
		t.Fatal("expected FK violation for missing VIRTUAL value, got nil")
	}
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("err = %v, want wrap of ErrConstraint", err)
	}
}
