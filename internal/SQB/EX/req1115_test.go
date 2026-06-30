//go:build !slt_corpus_full

package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestRowidColumn_MultiTableJoin reproduces REQ001115: x* VARCHAR column
// returns wrong value in multi-table joins.
// Uses exact SLT data from select4.test L31329-L31368.
func TestRowidColumn_MultiTableJoin(t *testing.T) {
	ResetForTest(t)

	ex := NewExecutor()
	ctx := context.Background()

	ex.RegisterTableWithPK("t3", []string{"a3", "b3", "c3", "d3", "e3", "x3"}, "")
	ex.RegisterTableWithPK("t2", []string{"a2", "b2", "c2", "d2", "e2", "x2"}, "")
	ex.RegisterTableWithPK("t4", []string{"a4", "b4", "c4", "d4", "e4", "x4"}, "")
	ex.RegisterTableWithPK("t9", []string{"a9", "b9", "c9", "d9", "e9", "x9"}, "")

	// Insert with exact SLT data matching the join conditions
	if _, err := ex.Exec(ctx, "INSERT INTO t3 VALUES (75, 272, 375, 984, 119, 'table tn3 row 89')"); err != nil {
		t.Fatalf("insert t3: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t2 VALUES (185, 220, 421, 753, 936, 'table tn2 row 89')"); err != nil {
		t.Fatalf("insert t2: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t4 VALUES (505, 847, 799, 797, 546, 'table tn4 row 106')"); err != nil {
		t.Fatalf("insert t4: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t9 VALUES (847, 19, 75, 129, 549, 'table tn9 row 62')"); err != nil {
		t.Fatalf("insert t9: %v", err)
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