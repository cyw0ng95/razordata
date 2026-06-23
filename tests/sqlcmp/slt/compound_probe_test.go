//go:build slt_corpus

package slt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestAllIndexesCausesFailure tests with all indexes created together.
func TestAllIndexesCausesFailure(t *testing.T) {
	root := corpusRoot()
	if _, err := os.Stat(root); err != nil {
		t.Skipf("corpus not present: %v", err)
	}

	driver := NewRazorDriver()
	if err := driver.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = driver.Close(context.Background()) })

	f, err := os.Open(filepath.Join(root, "select4.test"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	recs, err := Parse(f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	ctx := context.Background()

	// Load data
	for i := range recs {
		rec := &recs[i]
		if rec.Line >= 3136 {
			break
		}
		if rec.Kind == RecordStatementOK {
			_ = driver.Exec(ctx, rec.SQL)
		}
	}

	qCompound := "SELECT e1 FROM t1 WHERE a1 in (767,433,637,363,776,109,451) OR c1 in (683,531,654,246,3,876,309,284) OR (b1=738)\nEXCEPT\n  SELECT b8 FROM t8 WHERE NOT ((761=d8 AND b8=259 AND e8=44 AND 762=c8 AND 563=a8) OR e8 in (866,579,106,933))\nEXCEPT\n  SELECT e6 FROM t6 WHERE NOT ((825=b6 OR d6=500) OR (230=b6 AND e6=731 AND d6=355 AND 116=a6))\nUNION\n  SELECT b2 FROM t2 WHERE (d2=416)\nUNION\n  SELECT a4 FROM t4 WHERE c4 in (806,119,489,658,366,424,2,471) OR (215=c4 OR c4=424 OR e4=405)\nUNION ALL\n  SELECT a9 FROM t9 WHERE (e9=195) OR (c9=98 OR d9=145)\nUNION ALL\n  SELECT e5 FROM t5 WHERE (44=c5 AND a5=362 AND 193=b5) OR (858=b5)\nUNION\n  SELECT d3 FROM t3 WHERE (b3=152) OR (726=d3)\nUNION\n  SELECT e7 FROM t7 WHERE d7 in (687,507,603,52,118) OR (d7=399 AND e7=408 AND 396=b7 AND a7=97 AND c7=813) OR (e7=605 OR 837=b7 OR e7=918)"

	// Create ALL indexes
	allIndexes := []string{
		"CREATE INDEX t1i0 ON t1(a1,b1,c1,d1,e1,x1)",
		"CREATE INDEX t1i1 ON t1(b1,c1,d1,e1,x1)",
		"CREATE INDEX t1i2 ON t1(c1,d1,e1,x1)",
		"CREATE INDEX t1i3 ON t1(d1,e1,x1)",
		"CREATE INDEX t1i4 ON t1(e1,x1)",
		"CREATE INDEX t2a2 ON t2(a2)",
		"CREATE INDEX t2b2 ON t2(b2)",
		"CREATE INDEX t2c2 ON t2(c2)",
		"CREATE INDEX t2d2 ON t2(d2)",
		"CREATE INDEX t2e2 ON t2(e2)",
		"CREATE INDEX t3a3 ON t3(a3)",
		"CREATE INDEX t4b4 ON t4(b4)",
		"CREATE INDEX t5c5 ON t5(c5)",
		"CREATE INDEX t6d6 ON t6(d6)",
		"CREATE INDEX t7e7 ON t7(e7)",
	}
	for _, stmt := range allIndexes {
		if err := driver.Exec(ctx, stmt); err != nil {
			t.Logf("index failed: %v", err)
		}
	}

	rs, err := driver.Query(ctx, qCompound)
	if err != nil {
		t.Fatalf("query error with all indexes: %v", err)
	}
	t.Logf("With all 15 indexes: %d rows", len(rs.Rows))
	if len(rs.Rows) == 0 {
		fmt.Println("CONFIRMED: All 15 indexes together cause the compound query to return 0 rows")
		t.Errorf("Expected 41 rows, got 0")
	}

	// Binary search: create first half then second half
	for _, subset := range []struct {
		name  string
		start int
		end   int
	}{
		{"t1 indexes (0-4)", 0, 5},
		{"t2 indexes (5-9)", 5, 10},
		{"rest (10-14)", 10, 15},
	} {
		d2 := NewRazorDriver()
		if err := d2.Connect(context.Background()); err != nil {
			t.Fatalf("Connect2: %v", err)
		}
		t.Cleanup(func() { _ = d2.Close(context.Background()) })

		f2, _ := os.Open(filepath.Join(root, "select4.test"))
		recs2, _ := Parse(f2)
		f2.Close()
		for i := range recs2 {
			rec := &recs2[i]
			if rec.Line >= 3136 {
				break
			}
			if rec.Kind == RecordStatementOK {
				_ = d2.Exec(ctx, rec.SQL)
			}
		}

		for _, stmt := range allIndexes[subset.start:subset.end] {
			_ = d2.Exec(ctx, stmt)
		}

		rs2, err := d2.Query(ctx, qCompound)
		if err != nil {
			t.Logf("Subset %s: query error: %v", subset.name, err)
			continue
		}
		t.Logf("Subset %s (%d indexes): %d rows", subset.name, subset.end-subset.start, len(rs2.Rows))
	}
}
