//go:build !slt_corpus

package slt

import (
	"context"
	"strings"
	"testing"
)

// TestEndToEnd_SampleScript exercises the full driver + parser
// + runner pipeline against a hand-written script that mirrors
// the corpus style but does not require the corpus submodule.
// It is the "goes green without network access" smoke test.
func TestEndToEnd_SampleScript(t *testing.T) {
	script := `# Minimal SLT-style script. Mirrors the corpus grammar
# but stays self-contained so this test runs without the
# corpus submodule.

statement ok
CREATE TABLE t (id INT PRIMARY KEY, name TEXT)

statement ok
INSERT INTO t VALUES (1, 'alice')

statement ok
INSERT INTO t VALUES (2, 'bob')

statement ok
INSERT INTO t VALUES (3, 'carol')

query I rowsort label-ids
SELECT id FROM t
----
1
2
3

query T rowsort label-names
SELECT name FROM t
----
alice
bob
carol

query IT rowsort label-pairs
SELECT id, name FROM t
----
1 alice
2 bob
3 carol

# A query with the same label must hash to the same result;
# re-issuing the same query and asserting equivalence.
query I label-ids
SELECT id FROM t
----
1
2
3
`
	ctx := context.Background()
	d := NewRazorDriver()
	if err := d.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = d.Close(ctx) })

	recs, err := Parse(strings.NewReader(script))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := NewRunner(d, d.classifier)
	stats := r.Run(ctx, recs)
	if stats.Failed != 0 {
		t.Errorf("sample script failed: %+v", stats)
	}
	// 4 statements + 4 queries (1 with a re-labeled repeat) = 8
	// records plus 1 halt-equivalent at end. The label repeat
	// exercises the cross-query equivalence check.
	if stats.Passed < 7 {
		t.Errorf("expected >= 7 passes, got %+v", stats)
	}
}
