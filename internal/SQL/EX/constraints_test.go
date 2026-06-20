package EX

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
	ap "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

func ptr(s string) *string { return &s }

// TestConstraints_NotNull_InsertOK covers the happy path: when NOT NULL
// columns have values supplied, INSERT succeeds.
func TestConstraints_NotNull_InsertOK(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ct := NewCreateTable(&PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: 1, Nullable: false, PK: true},
			{Name: "name", Type: 1, Nullable: false},
		},
		PK: ptr("id"),
	})
	if _, err := ct.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("CREATE: %v", err)
	}
	ins, err := NewInsertWithStore(nil, "t", []string{"id", "name"}, [][]PS.Expr{
		{&PS.NumberLiteral{Val: 1}, &PS.StringLiteral{Val: "alice"}},
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewInsertWithStore: %v", err)
	}
	if _, err := ins.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Errorf("INSERT with values: %v", err)
	}
}

// TestConstraints_NotNull_InsertMissingValue_Rejected: INSERT omitting a
// NOT NULL column with no DEFAULT returns ErrConstraint.
func TestConstraints_NotNull_InsertMissingValue_Rejected(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ct := NewCreateTable(&PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: 1, Nullable: false, PK: true},
			{Name: "name", Type: 1, Nullable: false},
		},
		PK: ptr("id"),
	})
	if _, err := ct.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("CREATE: %v", err)
	}
	// Omit the `name` column.
	ins, err := NewInsertWithStore(nil, "t", []string{"id"}, [][]PS.Expr{
		{&PS.NumberLiteral{Val: 1}},
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewInsertWithStore: %v", err)
	}
	_, err = ins.Next(context.Background())
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("INSERT omitting NOT NULL col: got %v, want ErrConstraint", err)
	}
	if !strings.Contains(err.Error(), `"name"`) {
		t.Errorf("error should mention column name, got %q", err.Error())
	}
}

// TestConstraints_Default_InsertFills: omitted column with DEFAULT is
// recorded as having a default. fillDefaults is unit-tested below; this
// test exercises the registration path.
func TestConstraints_Default_InsertFills(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ct := NewCreateTable(&PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: 1, Nullable: false, PK: true},
			{Name: "count", Type: 1, Default: &PS.NumberLiteral{Val: 42}},
		},
		PK: ptr("id"),
	})
	if _, err := ct.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("CREATE: %v", err)
	}
	ss, ok := schemaFor("t")
	if !ok {
		t.Fatal("schema not found")
	}
	if len(ss.defaults) != 2 || ss.defaults[1] == nil {
		t.Fatalf("expected default on col 1, got %v", ss.defaults)
	}
}

// TestConstraints_NotNull_PrimaryKey_Implied: PRIMARY KEY column is
// implicitly NOT NULL. INSERT with NULL PK returns ErrConstraint.
func TestConstraints_NotNull_PrimaryKey_Implied(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	// Don't set Nullable=false explicitly; let PRIMARY KEY imply it.
	ct := NewCreateTable(&PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: 1, PK: true},
		},
		PK: ptr("id"),
	})
	if _, err := ct.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("CREATE: %v", err)
	}
	// Omit the PK column entirely.
	ins, err := NewInsertWithStore(nil, "t", []string{}, [][]PS.Expr{
		{nil},
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewInsertWithStore: %v", err)
	}
	_, err = ins.Next(context.Background())
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("NULL PK: got %v, want ErrConstraint", err)
	}
}

// TestConstraints_Nullable_ColumnAcceptsNull: a column without NOT NULL
// (default) accepts omitted values.
func TestConstraints_Nullable_ColumnAcceptsNull(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ct := NewCreateTable(&PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: 1, Nullable: false, PK: true},
			{Name: "name", Type: 1, Nullable: true},
		},
		PK: ptr("id"),
	})
	if _, err := ct.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("CREATE: %v", err)
	}
	ins, err := NewInsertWithStore(nil, "t", []string{"id"}, [][]PS.Expr{
		{&PS.NumberLiteral{Val: 1}},
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewInsertWithStore: %v", err)
	}
	if _, err := ins.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Errorf("INSERT omitting nullable col: %v", err)
	}
}

// TestConstraints_Update_NotNull: UPDATE that sets a NOT NULL column
// to NULL is rejected.
func TestConstraints_Update_NotNull(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ct := NewCreateTable(&PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: 1, Nullable: false, PK: true},
			{Name: "name", Type: 1, Nullable: false},
		},
		PK: ptr("id"),
	})
	if _, err := ct.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("CREATE: %v", err)
	}
	// Seed a row.
	ins, _ := NewInsertWithStore(nil, "t", []string{"id", "name"}, [][]PS.Expr{
		{&PS.NumberLiteral{Val: 1}, &PS.StringLiteral{Val: "a"}},
	}, nil, nil)
	if _, err := ins.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("seed INSERT: %v", err)
	}
	// Attempt to set name = NULL via UPDATE.
	scan := NewSeqScan("t")
	upd := NewUpdate("t", []PS.Pair{
		{Col: "name", Val: &PS.NullLiteral{}},
	}, nil, scan, nil)
	_, err := upd.Next(context.Background())
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("UPDATE setting NOT NULL to NULL: got %v, want ErrConstraint", err)
	}
}

// TestConstraints_FillDefaults_LiteralInt: DEFAULT with a numeric
// literal fills the column.
func TestConstraints_FillDefaults_LiteralInt(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ss := &storeSchema{
		cols:     []string{"a", "b"},
		pk:       "",
		nullable: []bool{true, true},
		defaults: []PS.Expr{nil, &PS.NumberLiteral{Val: 99}},
	}
	row := Row{Data: []any{int64(1), nil}}
	out, err := fillDefaults(ss, row)
	if err != nil {
		t.Fatalf("fillDefaults: %v", err)
	}
	if out.Data[1] != int64(99) {
		t.Errorf("expected default 99, got %v", out.Data[1])
	}
}

// TestConstraints_FillDefaults_NullLiteral: DEFAULT NULL still counts
// as a default and the column is marked nullable, so validateRow
// accepts it.
func TestConstraints_FillDefaults_NullLiteral(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ss := &storeSchema{
		cols:     []string{"a"},
		pk:       "",
		nullable: []bool{true},
		defaults: []PS.Expr{&PS.NullLiteral{}},
	}
	row := Row{Data: []any{nil}}
	out, err := fillDefaults(ss, row)
	if err != nil {
		t.Fatalf("fillDefaults: %v", err)
	}
	if err := validateRow(ss, out); err != nil {
		t.Errorf("validateRow after default NULL fill: %v", err)
	}
}

// TestConstraints_FillDefaults_NilSchema: schema with no defaults slice
// returns the row unchanged (no-op).
func TestConstraints_FillDefaults_NilSchema(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ss := &storeSchema{cols: []string{"a"}, nullable: []bool{true}}
	row := Row{Data: []any{int64(1)}}
	out, err := fillDefaults(ss, row)
	if err != nil {
		t.Fatalf("fillDefaults: %v", err)
	}
	if out.Data[0] != int64(1) {
		t.Errorf("expected unchanged value, got %v", out.Data[0])
	}
}

// TestConstraints_ValidateRow_RejectsNullNotNull: non-nullable column
// with nil value is rejected.
func TestConstraints_ValidateRow_RejectsNullNotNull(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ss := &storeSchema{
		cols:     []string{"a", "b"},
		pk:       "",
		nullable: []bool{false, true},
		defaults: nil,
	}
	row := Row{Data: []any{nil, int64(2)}}
	err := validateRow(ss, row)
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("validateRow: got %v, want ErrConstraint", err)
	}
	if !strings.Contains(err.Error(), `"a"`) {
		t.Errorf("error should mention col a, got %q", err.Error())
	}
}

// TestConstraints_ValidateRow_AcceptsNullNullable: nullable column with
// nil is accepted.
func TestConstraints_ValidateRow_AcceptsNullNullable(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ss := &storeSchema{
		cols:     []string{"a", "b"},
		pk:       "",
		nullable: []bool{true, true},
	}
	row := Row{Data: []any{nil, int64(2)}}
	if err := validateRow(ss, row); err != nil {
		t.Errorf("validateRow: %v", err)
	}
}

// TestConstraints_E2E_CreateTable_PropagatesConstraints: SQL DDL via the
// executor should produce a storeSchema with the right NOT NULL/DEFAULT
// fields.
func TestConstraints_E2E_CreateTable_PropagatesConstraints(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE users (id INTEGER NOT NULL, name TEXT NOT NULL, score INTEGER DEFAULT 0)"); err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	ss, ok := schemaFor("users")
	if !ok {
		t.Fatal("schema not found")
	}
	if len(ss.cols) != 3 {
		t.Fatalf("expected 3 cols, got %d", len(ss.cols))
	}
	if !(!ss.nullable[0] && !ss.nullable[1]) {
		t.Errorf("id/name should be NOT NULL, got %v", ss.nullable)
	}
	if !ss.nullable[2] {
		t.Errorf("score should be nullable (no NOT NULL), got %v", ss.nullable[2])
	}
	if ss.defaults[2] == nil {
		t.Errorf("score should have DEFAULT, got nil")
	}
}

// TestUnique_ColumnLevel_DuplicateRejected: a column declared UNIQUE
// rejects a second row with the same value.
func TestUnique_ColumnLevel_DuplicateRejected(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ct := NewCreateTable(&PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: 1, Nullable: false, PK: true},
			{Name: "email", Type: 1, Unique: true},
		},
		PK: ptr("id"),
	})
	if _, err := ct.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("CREATE: %v", err)
	}
	ins, err := NewInsertWithStore(nil, "t", []string{"id", "email"}, [][]PS.Expr{
		{&PS.NumberLiteral{Val: 1}, &PS.StringLiteral{Val: "a@x"}},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ins.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("first INSERT: %v", err)
	}
	// Second insert with same email → ErrConstraint.
	ins2, _ := NewInsertWithStore(nil, "t", []string{"id", "email"}, [][]PS.Expr{
		{&PS.NumberLiteral{Val: 2}, &PS.StringLiteral{Val: "a@x"}},
	}, nil, nil)
	_, err = ins2.Next(context.Background())
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("duplicate UNIQUE: got %v, want ErrConstraint", err)
	}
}

// TestUnique_ColumnLevel_DistinctValuesOK: distinct values on UNIQUE
// column are accepted.
func TestUnique_ColumnLevel_DistinctValuesOK(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ct := NewCreateTable(&PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: 1, Nullable: false, PK: true},
			{Name: "email", Type: 1, Unique: true},
		},
		PK: ptr("id"),
	})
	if _, err := ct.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("CREATE: %v", err)
	}
	ins, _ := NewInsertWithStore(nil, "t", []string{"id", "email"}, [][]PS.Expr{
		{&PS.NumberLiteral{Val: 1}, &PS.StringLiteral{Val: "a@x"}},
		{&PS.NumberLiteral{Val: 2}, &PS.StringLiteral{Val: "b@x"}},
	}, nil, nil)
	if _, err := ins.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Errorf("distinct emails: %v", err)
	}
}

// TestUnique_Composite_PartialMatchAllowed: composite UNIQUE (a, b)
// allows (1, 'x') and (1, 'y') — partial matches are OK.
func TestUnique_Composite_PartialMatchAllowed(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ct := NewCreateTable(&PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: 1, Nullable: false, PK: true},
			{Name: "a", Type: 1},
			{Name: "b", Type: 1},
		},
		PK:                ptr("id"),
		UniqueConstraints: []PS.UniqueKey{{Cols: []string{"a", "b"}}},
	})
	if _, err := ct.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("CREATE: %v", err)
	}
	ss, ok := schemaFor("t")
	if !ok {
		t.Fatal("schema not found")
	}
	if len(ss.unique) != 1 || len(ss.unique[0].Cols) != 2 {
		t.Fatalf("expected 1 composite unique, got %v", ss.unique)
	}
	if ss.unique[0].Cols[0] != 1 || ss.unique[0].Cols[1] != 2 {
		t.Errorf("composite indices wrong: %v", ss.unique[0].Cols)
	}
}

// TestUnique_Composite_FullMatchRejected: composite UNIQUE (a, b)
// rejects (1, 'x') then (1, 'x').
func TestUnique_Composite_FullMatchRejected(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ct := NewCreateTable(&PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: 1, Nullable: false, PK: true},
			{Name: "a", Type: 1},
			{Name: "b", Type: 1},
		},
		PK:                ptr("id"),
		UniqueConstraints: []PS.UniqueKey{{Cols: []string{"a", "b"}}},
	})
	if _, err := ct.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("CREATE: %v", err)
	}
	ins, _ := NewInsertWithStore(nil, "t", []string{"id", "a", "b"}, [][]PS.Expr{
		{&PS.NumberLiteral{Val: 1}, &PS.NumberLiteral{Val: 1}, &PS.StringLiteral{Val: "x"}},
		{&PS.NumberLiteral{Val: 2}, &PS.NumberLiteral{Val: 1}, &PS.StringLiteral{Val: "x"}},
	}, nil, nil)
	_, err := ins.Next(context.Background())
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("composite duplicate: got %v, want ErrConstraint", err)
	}
}

// TestUnique_WithinStatement: multi-row INSERT with duplicates
// in the batch returns ErrConstraint.
func TestUnique_WithinStatement(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ct := NewCreateTable(&PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: 1, Nullable: false, PK: true},
			{Name: "email", Type: 1, Unique: true},
		},
		PK: ptr("id"),
	})
	if _, err := ct.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("CREATE: %v", err)
	}
	ins, _ := NewInsertWithStore(nil, "t", []string{"id", "email"}, [][]PS.Expr{
		{&PS.NumberLiteral{Val: 1}, &PS.StringLiteral{Val: "a@x"}},
		{&PS.NumberLiteral{Val: 2}, &PS.StringLiteral{Val: "a@x"}}, // dup within batch
	}, nil, nil)
	_, err := ins.Next(context.Background())
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("within-statement duplicate: got %v, want ErrConstraint", err)
	}
}

// TestUnique_PrimaryKeyImplied: PK is implicitly UNIQUE. Two rows
// with the same PK → ErrConstraint.
func TestUnique_PrimaryKeyImplied(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ct := NewCreateTable(&PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: 1, Nullable: false, PK: true},
		},
		PK: ptr("id"),
	})
	if _, err := ct.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("CREATE: %v", err)
	}
	ins, _ := NewInsertWithStore(nil, "t", []string{"id"}, [][]PS.Expr{
		{&PS.NumberLiteral{Val: 1}},
		{&PS.NumberLiteral{Val: 1}}, // duplicate PK
	}, nil, nil)
	_, err := ins.Next(context.Background())
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("duplicate PK: got %v, want ErrConstraint", err)
	}
}

// TestUnique_NullSkipped: NULL in a UNIQUE column does NOT trigger
// a uniqueness check (SQL standard). Multiple NULLs are allowed.
func TestUnique_NullSkipped(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ct := NewCreateTable(&PS.CreateTable{
		Name: "t",
		Cols: []PS.ColDef{
			{Name: "id", Type: 1, Nullable: false, PK: true},
			{Name: "tag", Type: 1, Unique: true, Nullable: true},
		},
		PK: ptr("id"),
	})
	if _, err := ct.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("CREATE: %v", err)
	}
	ins, _ := NewInsertWithStore(nil, "t", []string{"id"}, [][]PS.Expr{
		{&PS.NumberLiteral{Val: 1}}, // tag omitted → NULL
		{&PS.NumberLiteral{Val: 2}}, // tag omitted → NULL (allowed)
	}, nil, nil)
	if _, err := ins.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Errorf("multiple NULLs on UNIQUE: %v", err)
	}
}

// TestUnique_EncodeKey_Stable: encoding the same values produces the
// same key; different values produce different keys.
func TestUnique_EncodeKey_Stable(t *testing.T) {
	k1 := encodeUniqueKey([]int{0}, []any{int64(42)})
	k2 := encodeUniqueKey([]int{0}, []any{int64(42)})
	if string(k1) != string(k2) {
		t.Errorf("same values produced different keys: %x vs %x", k1, k2)
	}
	k3 := encodeUniqueKey([]int{0}, []any{int64(99)})
	if string(k1) == string(k3) {
		t.Errorf("different values produced same key: %x", k1)
	}
	// Composite: (a=1, b='x') vs (a=1, b='y') should differ.
	k4 := encodeUniqueKey([]int{0, 1}, []any{int64(1), "x"})
	k5 := encodeUniqueKey([]int{0, 1}, []any{int64(1), "y"})
	if string(k4) == string(k5) {
		t.Errorf("composite with different second col produced same key")
	}
}

// TestUnique_E2E_FullSQL: SQL DDL + DML through the executor.
func TestUnique_E2E_FullSQL(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE users (id INTEGER NOT NULL, email TEXT UNIQUE)"); err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO users VALUES (1, 'a@x')"); err != nil {
		t.Errorf("first INSERT: %v", err)
	}
	_, err := ex.Exec(ctx, "INSERT INTO users VALUES (2, 'a@x')")
	if !errors.Is(err, ap.ErrConstraint) {
		t.Errorf("second INSERT duplicate: got %v, want ErrConstraint", err)
	}
}
