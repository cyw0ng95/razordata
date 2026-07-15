//go:build !slt_corpus_full

package EX

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// REQ001440 / REQ001456: end-to-end QueryAll benchmark comparing the
// vectorized path (SeqScan + Project → tryVectorizePlan → Vec) with
// the row path (SeqScan + Project as-is). The kernel reuses the
// same per-element work; the only difference is the boundary
// cost (per-row vs per-batch materialisation). At 1024 rows the
// vectorized path should win because it amortises the
// per-Next overhead across the whole batch.
//
// REQ001440's improvement is changing BatchToRowAdapter from
// per-row conversion to per-batch ToRows. Before the change each
// Next() allocated a Row + Cols slice + Data slice; after the
// change the cost is paid once per 1024-row batch.
func BenchmarkQueryAll_VecVsRow_1KRows(b *testing.B) {
	const rowCount = 1024
	ResetForTest(b)
	ex, eng := newEngineExecutor(b)
	defer eng.Close()
	ctx := context.Background()
	// For the planning circuit to take the Vec path, the query
	// must be SeqScan + Project on a single registered table.
	if _, err := ex.Exec(ctx, "CREATE TABLE bench (a INTEGER PRIMARY KEY, b INTEGER, c TEXT)"); err != nil {
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			b.Fatalf("CREATE: %v", err)
		}
	}
	for i := 0; i < rowCount; i++ {
		ins := fmt.Sprintf("INSERT INTO bench VALUES (%d, %d, 'row%d')", i, i*10, i)
		if _, err := ex.Exec(ctx, ins); err != nil {
			b.Fatalf("INSERT %d: %v", i, err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := ex.QueryAll(ctx, "SELECT b FROM bench")
		if err != nil {
			b.Fatal(err)
		}
		if len(rows) != rowCount {
			b.Fatalf("rows: %d want %d", len(rows), rowCount)
		}
	}
}
