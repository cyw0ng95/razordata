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
	})
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
	})
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
	})
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
	})
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
	})
	if _, err := ins.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("seed INSERT: %v", err)
	}
	// Attempt to set name = NULL via UPDATE.
	scan := NewSeqScan("t")
	upd := NewUpdate("t", []PS.Pair{
		{Col: "name", Val: &PS.NullLiteral{}},
	}, nil, scan)
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
	row := Row{Data: []interface{}{int64(1), nil}}
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
	row := Row{Data: []interface{}{nil}}
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
	row := Row{Data: []interface{}{int64(1)}}
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
	row := Row{Data: []interface{}{nil, int64(2)}}
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
	row := Row{Data: []interface{}{nil, int64(2)}}
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
