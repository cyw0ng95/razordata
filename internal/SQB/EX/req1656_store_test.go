package EX

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"sort"
	"strconv"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestCrossJoin_StoreEngineRepro uses a store-backed engine (like the
// SLT runner) to reproduce the hash mismatch that only appears in the
// SLT runner path but not in the standalone executor path.
func TestCrossJoin_StoreEngineRepro(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()

	setupSQLs := []string{
		"CREATE TABLE tab0(col0 INTEGER, col1 INTEGER, col2 INTEGER)",
		"CREATE TABLE tab1(col0 INTEGER, col1 INTEGER, col2 INTEGER)",
		"CREATE TABLE tab2(col0 INTEGER, col1 INTEGER, col2 INTEGER)",
		"INSERT INTO tab0 VALUES(89,91,82)",
		"INSERT INTO tab0 VALUES(35,97,1)",
		"INSERT INTO tab0 VALUES(24,86,33)",
		"INSERT INTO tab1 VALUES(64,10,57)",
		"INSERT INTO tab1 VALUES(3,26,54)",
		"INSERT INTO tab1 VALUES(80,13,96)",
		"INSERT INTO tab2 VALUES(7,31,27)",
		"INSERT INTO tab2 VALUES(79,17,38)",
		"INSERT INTO tab2 VALUES(78,59,26)",
	}

	// Precompile like the SLT runner
	allSQLs := append([]string{}, setupSQLs...)
	allSQLs = append(allSQLs,
		"SELECT 76 * - cor0.col2 col2 FROM tab0, tab1 AS cor0",
		"SELECT ALL cor0.col1 AS col1 FROM tab0, tab2 AS cor0, tab0 AS cor1",
		"SELECT tab1.col1 FROM tab0, tab1 AS cor0 CROSS JOIN tab1",
		"SELECT ALL - cor0.col0 FROM tab1, tab2 AS cor0, tab1 AS cor1",
	)
	ex.Precompile(ctx, allSQLs)

	// Execute setup
	for _, s := range setupSQLs {
		mustExec(t, ex, ctx, s)
	}

	queries := []struct {
		sql      string
		wantHash string
		wantN    int
	}{
		{
			sql:      "SELECT 76 * - cor0.col2 col2 FROM tab0, tab1 AS cor0",
			wantHash: "7c7275be2a652135d8807f37aa5bda8f",
			wantN:    9,
		},
		{
			sql:      "SELECT tab1.col1 FROM tab0, tab1 AS cor0 CROSS JOIN tab1",
			wantHash: "d671a064e2da709ca4cdfea317b8e892",
			wantN:    27,
		},
		{
			sql:      "SELECT ALL cor0.col1 AS col1 FROM tab0, tab2 AS cor0, tab0 AS cor1",
			wantHash: "7599b480125de521efed71b5b2413c7d",
			wantN:    27,
		},
		{
			sql:      "SELECT ALL - cor0.col0 FROM tab1, tab2 AS cor0, tab1 AS cor1",
			wantHash: "c82df1de3cb666224690a83f3d790d79",
			wantN:    27,
		},
	}

	for _, q := range queries {
		rows, err := ex.QueryAll(ctx, q.sql)
		if err != nil {
			t.Errorf("query %q: %v", q.sql, err)
			continue
		}
		gotHash, gotN := hashRowsStore(rows)
		t.Logf("SQL: %s\n  rows=%d cells=%d hash=%s (want %d cells, hash=%s)",
			q.sql, len(rows), gotN, gotHash, q.wantN, q.wantHash)
		if gotHash != q.wantHash {
			vals := make([]string, 0, gotN)
			for _, r := range rows {
				for _, v := range r.Data {
					vals = append(vals, formatValStore(v))
				}
			}
			sort.Strings(vals)
			t.Errorf("  hash mismatch: got %s, want %s\n  values: %v", gotHash, q.wantHash, vals)
		}
	}
}

func hashRowsStore(rows []DT.Row) (string, int) {
	if len(rows) == 0 {
		return "", 0
	}
	vals := make([]string, 0, len(rows)*len(rows[0].Data))
	for _, r := range rows {
		for _, v := range r.Data {
			vals = append(vals, formatValStore(v))
		}
	}
	sort.Strings(vals)
	h := md5.New()
	for _, v := range vals {
		h.Write([]byte(v))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)), len(vals)
}

func formatValStore(v DT.Value) string {
	switch v.Kind {
	case 1: // KindInt
		return strconv.FormatInt(v.I64, 10)
	case 2: // KindFloat
		return strconv.FormatFloat(v.F64, 'f', 3, 64)
	default:
		if s, ok := v.ToAny().(string); ok {
			return s
		}
		return ""
	}
}
