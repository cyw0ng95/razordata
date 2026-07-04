//go:build slt_corpus

package EX

import (
	"bufio"
	"context"
	"os"
	"strings"
	"testing"
)

func TestREQ001162_ExactFailingQuery(t *testing.T) {
	ResetForTest(t)
	ex := NewExecutor()
	ctx := context.Background()

	// Load the SLT corpus
	f, err := os.Open("/workspace/tests/sqlcmp/corpus/test/select4.test")
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var currentSQL strings.Builder
	inStatement := false
	statementOK := false

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
					if strings.HasPrefix(upper, "CREATE TABLE") || strings.HasPrefix(upper, "INSERT INTO") || strings.HasPrefix(upper, "CREATE INDEX") {
						_, err := ex.Exec(ctx, sql)
						if err != nil {
							t.Logf("skip error: %v", err)
						}
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
			} else {
				currentSQL.WriteString(line)
				currentSQL.WriteString(" ")
			}
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

	// The exact failing query from the diagnostic output
	query := `SELECT b2, d6, b9*398, c1, e3*353+b9 FROM t1, t9, t6, t3, t2 WHERE d6 in (885,924,457,578,786,664) AND 488=d2 AND a3=b9 AND c9=688 AND a1=d9`
	rows, err := ex.QueryAll(ctx, query)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	t.Logf("got %d rows", len(rows))
	for i, row := range rows {
		if i < 5 {
			t.Logf("row %d: Data=%v", i, row.Data)
		}
	}
	
	// Expected: 30 cells (6 rows * 5 columns)
	if len(rows) == 0 {
		t.Fatalf("got 0 rows, expected non-zero")
	}
}
