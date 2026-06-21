//go:build slt_fuzz

package slt

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// FuzzParser feeds the parser with random SLT-shaped text.
// The goal is "no panic, no goroutine leak, no infinite
// loop" on arbitrary input. We do not assert anything on
// the parsed records because the parser is intentionally
// tolerant (it surfaces bad records as RecordInvalid).
//
// Build tag `slt_fuzz` keeps this out of the default test
// run. Invoke with:
//
//	go test -fuzz FuzzParser -fuzztime 60s -tags slt_fuzz \
//	    ./tests/sqlcmp/slt/...
func FuzzParser(f *testing.F) {
	// Seed corpus: a few known-good and known-bad inputs.
	f.Add("statement ok\nSELECT 1\n")
	f.Add("query I rowsort\nSELECT 1\n----\n1\n")
	f.Add("# only a comment\n")
	f.Add("garbage")
	f.Fuzz(func(t *testing.T, seed string) {
		// The fuzz input is the seed; we mutate it to widen
		// coverage (raw fuzz input may be short and miss
		// edge cases that random concatenation catches).
		var buf bytes.Buffer
		buf.WriteString(seed)
		for i := 0; i < 8; i++ {
			buf.WriteByte('\n')
			buf.WriteString(randLine(seed))
		}
		_, _ = Parse(strings.NewReader(buf.String()))
	})
}

func randLine(s string) string {
	if len(s) == 0 {
		return "statement ok\nSELECT 1"
	}
	rng := rand.New(rand.NewSource(int64(len(s))))
	choices := []string{
		"statement ok",
		"statement error",
		"query I",
		"query T rowsort",
		"halt",
		"hash-threshold 5",
		"skipif razor",
		"onlyif razor",
		"# random comment",
		"NOT A VALID HEADER",
		"",
	}
	choice := choices[rng.Intn(len(choices))]
	if choice == "" {
		return ""
	}
	return fmt.Sprintf("%s\n%s", choice, s[:1+rng.Intn(len(s))])
}
