//go:build slt_corpus

package slt

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// loadCorpusSLT parses a single SLT .test file (e.g. "evidence/in1") and
// returns its parsed records.
func loadCorpusSLT(b *testing.B, rel string) []Record {
	b.Helper()
	full := filepath.Join(corpusRoot(), rel+".test")
	f, err := os.Open(full)
	if err != nil {
		b.Skipf("corpus file not present at %s: %v", full, err)
	}
	defer f.Close()
	recs, err := Parse(f)
	if err != nil {
		b.Fatalf("parse %s: %v", full, err)
	}
	return recs
}

// newSLTDriver constructs a RazorDriver, connects, and registers cleanup.
func newSLTDriver(b *testing.B) *RazorDriver {
	b.Helper()
	d := NewRazorDriver()
	ctx, cancel := context.WithTimeout(context.Background(), 30_000_000_000)
	defer cancel()
	if err := d.Connect(ctx); err != nil {
		b.Fatalf("driver.Connect: %v", err)
	}
	b.Cleanup(func() { _ = d.Close(context.Background()) })
	return d
}

// BenchmarkSLT_In1 runs every record in evidence/in1.test via the SLT
// runner. The benchmark loop drives Reset() between iterations so each
// iteration begins from a clean catalog. This exercises scalar IN (list),
// scalar IN subquery, NOT IN, and IN-on-correlated-subquery paths end-to-end.
func BenchmarkSLT_In1(b *testing.B) {
	recs := loadCorpusSLT(b, "evidence/in1")
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

// BenchmarkSLT_In2 mirrors BenchmarkSLT_In1 for evidence/in2.test.
// in2 covers IN / NOT IN against small typed tables (INTEGER + TEXT/VARCHAR).
func BenchmarkSLT_In2(b *testing.B) {
	recs := loadCorpusSLT(b, "evidence/in2")
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

// BenchmarkSLT_Update runs every record in evidence/slt_lang_update.test
// via the SLT runner. Covers CREATE TABLE, INSERT, 13 UPDATE patterns,
// and SELECT-verify interleaving.
func BenchmarkSLT_Update(b *testing.B) {
	recs := loadCorpusSLT(b, "evidence/slt_lang_update")
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

// BenchmarkSLT_CreateTrigger runs evidence/slt_lang_createtrigger.test.
// Covers CREATE/DROP TRIGGER, DDL path, and trigger registration.
func BenchmarkSLT_CreateTrigger(b *testing.B) {
	recs := loadCorpusSLT(b, "evidence/slt_lang_createtrigger")
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

// BenchmarkSLT_AggFunc runs evidence/slt_lang_aggfunc.test.
// Covers aggregate functions: sum, count, avg, min, max, group_concat,
// total, DISTINCT variants, and aggregate operator paths.
func BenchmarkSLT_AggFunc(b *testing.B) {
	recs := loadCorpusSLT(b, "evidence/slt_lang_aggfunc")
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

// BenchmarkSLT_CreateView runs evidence/slt_lang_createview.test.
// Covers CREATE/DROP VIEW, INSERT/UPDATE through views, and
// view resolution during query planning.
func BenchmarkSLT_CreateView(b *testing.B) {
	recs := loadCorpusSLT(b, "evidence/slt_lang_createview")
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