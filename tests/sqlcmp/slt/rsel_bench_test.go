//go:build slt_corpus

package slt

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkSLT_RSel125 runs the records in random/select/slt_good_125
// via the SLT runner. This file is purely DML (CREATE TABLE × 3 +
// INSERT × 9), exercising the write path end-to-end. hash-threshold 8
// hint makes it a candidate for the parallel hash join path once
// queries are added.
func BenchmarkSLT_RSel125(b *testing.B) {
	recs := loadCorpusSLT(b, "random/select/slt_good_125")
	d := newSLTDriver(b)
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		if err := d.Reset(ctx); err != nil {
			b.Fatal(err)
		}
		runner := NewRunner(d, d.classifier, RazorEngineName)
		_ = runner.Run(ctx, recs)
	}
}

// BenchmarkSLT_RSel126 mirrors BenchmarkSLT_RSel125 for
// random/select/slt_good_126 (identical setup layout).
func BenchmarkSLT_RSel126(b *testing.B) {
	recs := loadCorpusSLT(b, "random/select/slt_good_126")
	d := newSLTDriver(b)
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		if err := d.Reset(ctx); err != nil {
			b.Fatal(err)
		}
		runner := NewRunner(d, d.classifier, RazorEngineName)
		_ = runner.Run(ctx, recs)
	}
}

// ensureCorpusFile is a tiny helper to keep test failure modes clear
// when an expected corpus file is absent. Currently unused but kept
// for the bench's loadCorpusSLT skip semantics.
func ensureCorpusFile(b *testing.B, rel string) {
	b.Helper()
	full := filepath.Join(corpusRoot(), rel+".test")
	if _, err := os.Stat(full); err != nil {
		b.Skipf("corpus file not present at %s: %v", full, err)
	}
}