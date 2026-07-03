package EX

import (
	"bufio"
	"context"
	"os"
	"strings"
	"testing"
)

// REQ001192: Reproduce the first failing query from select5.test
// with ALL 64 tables loaded (matching SLT runner setup).
func TestReq001192_FullSetup(t *testing.T) {
	ResetForTest(t)
	ctx := context.Background()
	// Use store-backed executor to match SLT runner path more closely.
	ex, _ := newEngineExecutor(t)

	// Load the full select5.test setup
	paths := []string{
		"../../../../tests/sqlcmp/corpus/test/select5.test",
		"../../../tests/sqlcmp/corpus/test/select5.test",
		"../../tests/sqlcmp/corpus/test/select5.test",
		"../tests/sqlcmp/corpus/test/select5.test",
		"tests/sqlcmp/corpus/test/select5.test",
		"/home/cyw0ng/projects/razordata/tests/sqlcmp/corpus/test/select5.test",
	}
	var f *os.File
	for _, p := range paths {
		if fh, err := os.Open(p); err == nil {
			f = fh
			break
		}
	}
	if f == nil {
		t.Skipf("corpus not found")
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var currentSQL strings.Builder
	inStatement := false
	statementOK := false
	insCount := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "statement ok" {
			inStatement = true
			statementOK = true
			currentSQL.Reset()
			continue
		}
		if line == "statement error" {
			inStatement = true
			statementOK = false
			currentSQL.Reset()
			continue
		}
		if inStatement {
			if line == "" || strings.HasPrefix(line, "query ") || strings.HasPrefix(line, "----") {
				sql := strings.TrimSpace(currentSQL.String())
				if sql != "" && statementOK {
					upper := strings.ToUpper(sql)
					if strings.HasPrefix(upper, "CREATE TABLE") || strings.HasPrefix(upper, "INSERT INTO") {
						if _, err := ex.Exec(ctx, sql); err != nil {
							t.Fatalf("exec error: %v (sql: %s)", err, sql[:min(80, len(sql))])
						}
						insCount++
					}
				}
				inStatement = false
				currentSQL.Reset()
				if strings.HasPrefix(line, "query ") {
					for scanner.Scan() {
						l := strings.TrimSpace(scanner.Text())
						if l == "----" {
							break
						}
					}
				}
				continue
			}
			currentSQL.WriteString(line)
			currentSQL.WriteString(" ")
			continue
		}
		if strings.HasPrefix(line, "query ") {
			for scanner.Scan() {
				l := strings.TrimSpace(scanner.Text())
				if l == "----" {
					break
				}
			}
		}
	}
	t.Logf("loaded %d statements", insCount)

	// Run the exact failing query
	query := `SELECT x29,x31,x51,x55 FROM t51,t29,t31,t55 WHERE a51=b31 AND a29=6 AND a29=b51 AND b55=a31`
	rows, err := ex.QueryAll(ctx, query)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	t.Logf("got %d rows", len(rows))
	for i, r := range rows {
		if i < 5 {
			t.Logf("row %d: %v", i, r.Data)
		}
	}
	if len(rows) == 0 {
		t.Errorf("got 0 rows, expected 1")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
