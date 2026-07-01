//go:build !slt_corpus_full

package EX

import (
	"context"
	"runtime"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// REQ000941: MIN(-col0) with table alias.
func TestReq000941_AliasWithUnaryMinus(t *testing.T) {
	ctx := context.Background()
	ex := NewExecutorWithEngine(nil)
	for _, s := range []string{
		"CREATE TABLE tab1(col0 INTEGER, col1 INTEGER, col2 INTEGER)",
		"INSERT INTO tab1 VALUES(51,14,96)",
		"INSERT INTO tab1 VALUES(85,5,59)",
		"INSERT INTO tab1 VALUES(91,47,68)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT MIN(- col0) FROM tab1 AS cor0")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Data[0].Kind == 0 {
		t.Errorf("MIN(- col0) with alias: got %v, want -91", rows[0].Data[0].ToAny())
	}
	if rows[0].Data[0].ToAny() != int64(-91) {
		t.Errorf("MIN(- col0) with alias: got %v, want -91", rows[0].Data[0].ToAny())
	}
	rows, err = ex.QueryAll(ctx, "SELECT DISTINCT - MIN( DISTINCT - col0 ) * - 30 + COUNT( * ) AS col1 FROM tab1 AS cor0 WHERE + col0 IS NOT NULL")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("REQ000941: expected 1 row, got %d", len(rows))
	}
	v := rows[0].Data[0].ToAny()
	if v != int64(-2727) {
		t.Errorf("REQ000941: got %v, want -2727", v)
	}
}

func TestReq000941_UnaryMinusWithAlias(t *testing.T) {
	ctx := context.Background()
	ex := NewExecutorWithEngine(nil)
	for _, s := range []string{
		"CREATE TABLE tab2(col0 INTEGER, col1 INTEGER, col2 INTEGER)",
		"INSERT INTO tab2 VALUES(51,14,96)",
		"INSERT INTO tab2 VALUES(85,5,59)",
		"INSERT INTO tab2 VALUES(91,47,68)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT - col0 FROM tab2 AS cor0")
	if err != nil {
		t.Fatal(err)
	}
	expected := []int64{-51, -85, -91}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	for i, r := range rows {
		v := r.Data[0].ToAny()
		if v != expected[i] {
			t.Errorf("row %d: got %v, want %v", i, v, expected[i])
		}
	}
}

// REQ000985: EncodeRow pooled buffer allocation check.
func TestStore_EncodeRowNoAlloc(t *testing.T) {
	_ = DT.RegisterStoreSchema("enc_test", []string{"id", "name", "val"}, "id")
	ss, _ := DT.SchemaFor("enc_test")
	row := DT.Row{
		Data: []DT.Value{
			DT.NewIntValue(42),
			DT.NewTextValue("hello"),
			DT.NewFloatValue(3.14),
		},
	}
	for i := 0; i < 10; i++ {
		_, _ = OP.EncodeRow(ss, row)
	}
	runtime.GC()
	runtime.GC()
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = OP.EncodeRow(ss, row)
	})
	if allocs > 1 {
		t.Errorf("EncodeRow allocated %.1f allocs/call, want <= 1", allocs)
	}
}

// REQ001061: DML on updatable vs non-updatable views.
func TestREQ001061_DMLOnView(t *testing.T) {
	ctx := context.Background()
	ex := NewExecutor()
	_, err := ex.Exec(ctx, "CREATE TABLE t (id INT, val TEXT)")
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	_, err = ex.Exec(ctx, "CREATE VIEW v_agg AS SELECT count(*) FROM t")
	if err != nil {
		t.Fatalf("CREATE VIEW (agg): %v", err)
	}
	_, err = ex.Exec(ctx, "CREATE VIEW v_simple AS SELECT * FROM t")
	if err != nil {
		t.Fatalf("CREATE VIEW (simple): %v", err)
	}
	tests := []struct {
		name    string
		sql     string
		wantErr bool
	}{
		{"UPDATE agg view", "UPDATE v_agg SET val='x'", true},
		{"DELETE from agg view", "DELETE FROM v_agg", true},
		{"UPDATE simple view", "UPDATE v_simple SET val='x'", false},
		{"DELETE from simple view", "DELETE FROM v_simple", false},
		{"UPDATE table", "UPDATE t SET val='x'", false},
		{"DELETE from table", "DELETE FROM t", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ex.Exec(ctx, tt.sql)
			if (err != nil) != tt.wantErr {
				t.Errorf("Exec(%q) error = %v, wantErr %v", tt.sql, err, tt.wantErr)
			}
		})
	}
}

// REQ001062: REPLACE INTO row count.
func TestREQ001062_ReplaceIntoRowCount(t *testing.T) {
	ResetForTest(t)
	ctx := context.Background()
	ex := NewExecutor()
	_, err := ex.Exec(ctx, "CREATE TABLE t (id INT PRIMARY KEY, val TEXT)")
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	_, err = ex.Exec(ctx, "INSERT INTO t VALUES (1, 'a')")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	tests := []struct {
		name         string
		sql          string
		wantAffected int64
	}{
		{"REPLACE INTO existing", "REPLACE INTO t VALUES (1, 'b')", 2},
		{"REPLACE INTO new", "REPLACE INTO t VALUES (2, 'c')", 1},
		{"INSERT OR REPLACE existing", "INSERT OR REPLACE INTO t VALUES (1, 'd')", 2},
		{"INSERT OR REPLACE new", "INSERT OR REPLACE INTO t VALUES (3, 'e')", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := ex.Exec(ctx, tt.sql)
			if err != nil {
				t.Fatalf("Exec(%q): %v", tt.sql, err)
			}
			if res.RowsAffected != tt.wantAffected {
				t.Errorf("Exec(%q) RowsAffected = %d, want %d", tt.sql, res.RowsAffected, tt.wantAffected)
			}
		})
	}
}

// REQ001070: LIKE prefix detection for index usage.
func TestIndexedColumnLikePrefix(t *testing.T) {
	tests := []struct {
		name    string
		expr    PS.Expr
		wantCol string
		wantOK  bool
		wantPfx string
	}{
		{"simple prefix", &PS.BinaryExpr{Op: LX.T_LIKE, Left: &PS.Ident{Name: "name"}, Right: &PS.StringLiteral{Val: "abc%"}}, "name", true, "abc"},
		{"exact match", &PS.BinaryExpr{Op: LX.T_LIKE, Left: &PS.Ident{Name: "name"}, Right: &PS.StringLiteral{Val: "abc"}}, "name", true, "abc"},
		{"wildcard middle", &PS.BinaryExpr{Op: LX.T_LIKE, Left: &PS.Ident{Name: "name"}, Right: &PS.StringLiteral{Val: "ab%c"}}, "name", true, "ab"},
		{"leading wildcard", &PS.BinaryExpr{Op: LX.T_LIKE, Left: &PS.Ident{Name: "name"}, Right: &PS.StringLiteral{Val: "%abc"}}, "", false, ""},
		{"column on right", &PS.BinaryExpr{Op: LX.T_LIKE, Left: &PS.StringLiteral{Val: "abc%"}, Right: &PS.Ident{Name: "name"}}, "", false, ""},
		{"nil expr", nil, "", false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			col, prefix, ok := indexedColumnLikePrefix(tc.expr)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tc.wantOK)
			}
			if ok {
				if col != tc.wantCol {
					t.Errorf("col=%q, want %q", col, tc.wantCol)
				}
				if string(prefix) != tc.wantPfx {
					t.Errorf("prefix=%q, want %q", string(prefix), tc.wantPfx)
				}
			}
		})
	}
}

func TestIndexedColumnLikePrefix_Underscore(t *testing.T) {
	expr := &PS.BinaryExpr{Op: LX.T_LIKE, Left: &PS.Ident{Name: "name"}, Right: &PS.StringLiteral{Val: "abc_def%"}}
	col, prefix, ok := indexedColumnLikePrefix(expr)
	if !ok {
		t.Fatal("expected success")
	}
	if col != "name" {
		t.Errorf("col=%q", col)
	}
	if string(prefix) != "abc" {
		t.Errorf("prefix=%q", prefix)
	}
}

// REQ001072: Predicate pushdown into subqueries.
func TestPlanner_PredicatePushdownSubquery(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()
	ex.RegisterTable("t", []string{"id", "x", "y"})
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10, 100), (2, 20, 200), (3, 10, 300), (4, 30, 100)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT * FROM (SELECT * FROM t WHERE x > 10) sq WHERE y < 200 ORDER BY id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0].ToAny().(int64) != 4 {
		t.Errorf("expected id=4, got %v", rows[0].Data[0].ToAny())
	}
}

func TestPlanner_PredicatePushdownSubquery_NonFlattenable(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()
	ex.RegisterTable("t", []string{"id", "grp", "val"})
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 1, 10), (2, 1, 20), (3, 2, 30), (4, 2, 40)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT * FROM (SELECT grp, SUM(val) AS total FROM t GROUP BY grp) sq WHERE total > 30 ORDER BY grp")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[1].ToAny().(int64) != 70 {
		t.Errorf("expected total=70, got %v", rows[0].Data[1].ToAny())
	}
}

// REQ001114: IN-list predicate on cross-join with projected values.
func TestINList_Join_ProjectedValues(t *testing.T) {
	ResetForTest(t)
	ex := NewExecutor()
	ctx := context.Background()
	ex.RegisterTableWithPK("t3", []string{"a3", "b3", "c3", "d3", "e3", "x3"}, "")
	ex.RegisterTableWithPK("t7", []string{"a7", "b7", "c7", "d7", "e7", "x7"}, "")
	if _, err := ex.Exec(ctx, "INSERT INTO t3 VALUES (637, 549, 476, 645, 401, 'table tn3 row 29')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t3 VALUES (591, 41, 452, 403, 807, 'table tn3 row 44')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t3 VALUES (710, 790, 81, 923, 147, 'table tn3 row 91')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t3 VALUES (644, 740, 395, 482, 406, 'table tn3 row 81')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t7 VALUES (894, 996, 606, 105, 280, 'table tn7 row 103')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := ex.QueryAll(ctx, "SELECT e3+491, a7 FROM t3, t7 WHERE a3 IN (637,591,710,644) AND e7=280")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(rows))
	}
	got := make(map[int64]bool)
	for _, r := range rows {
		if r.Data[0].Kind != DT.KindInt {
			t.Errorf("expected KindInt for col 0, got %v", r.Data[0].Kind)
		}
		if r.Data[1].I64 != 894 {
			t.Errorf("a7: expected 894, got %d", r.Data[1].I64)
		}
		got[r.Data[0].I64] = true
	}
	for _, want := range []int64{892, 1298, 638, 897} {
		if !got[want] {
			t.Errorf("missing expected e3+491 value %d", want)
		}
	}
}

// REQ001115: x* VARCHAR column in multi-table join.
func TestRowidColumn_MultiTableJoin(t *testing.T) {
	ResetForTest(t)
	ex := NewExecutor()
	ctx := context.Background()
	ex.RegisterTableWithPK("t3", []string{"a3", "b3", "c3", "d3", "e3", "x3"}, "")
	ex.RegisterTableWithPK("t2", []string{"a2", "b2", "c2", "d2", "e2", "x2"}, "")
	ex.RegisterTableWithPK("t4", []string{"a4", "b4", "c4", "d4", "e4", "x4"}, "")
	ex.RegisterTableWithPK("t9", []string{"a9", "b9", "c9", "d9", "e9", "x9"}, "")
	if _, err := ex.Exec(ctx, "INSERT INTO t3 VALUES (75, 272, 375, 984, 119, 'table tn3 row 89')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t2 VALUES (185, 220, 421, 753, 936, 'table tn2 row 89')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t4 VALUES (505, 847, 799, 797, 546, 'table tn4 row 106')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t9 VALUES (847, 19, 75, 129, 549, 'table tn9 row 62')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := ex.QueryAll(ctx, "SELECT c2*895, b4, b9+651, x3 FROM t3, t2, t4, t9 WHERE a3=c9 AND a9 IN (273,11,982,567,450,847,830,953) AND d2=753 AND b4=a9")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	wantC2x := DT.NewIntValue(376795)
	wantB4 := DT.NewIntValue(847)
	wantB9x := DT.NewIntValue(670)
	wantX3 := DT.NewTextValue("table tn3 row 89")
	if rows[0].Data[0].Kind != wantC2x.Kind || rows[0].Data[0].I64 != wantC2x.I64 {
		t.Errorf("c2*895: got %v, want %v", rows[0].Data[0], wantC2x)
	}
	if rows[0].Data[1].Kind != wantB4.Kind || rows[0].Data[1].I64 != wantB4.I64 {
		t.Errorf("b4: got %v, want %v", rows[0].Data[1], wantB4)
	}
	if rows[0].Data[2].Kind != wantB9x.Kind || rows[0].Data[2].I64 != wantB9x.I64 {
		t.Errorf("b9+651: got %v, want %v", rows[0].Data[2], wantB9x)
	}
	if rows[0].Data[3].Kind != wantX3.Kind || rows[0].Data[3].S != wantX3.S {
		t.Errorf("x3: got %v, want %v", rows[0].Data[3], wantX3)
	}
}