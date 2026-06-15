package slt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverageSnapshot_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	files := []fileStat{
		{path: "a.test", stats: Stats{Total: 3, Passed: 2, Failed: 1}},
		{path: "b.test", stats: Stats{Total: 5, Passed: 5}},
		{path: "c.test", stats: Stats{Total: 2, Skipped: 2}},
	}
	total := Stats{Total: 10, Passed: 7, Failed: 1, Skipped: 2, Duration: 42}
	path := filepath.Join(dir, "cov.json")
	if err := writeCoverageSnapshot(path, files, total, 0.5); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var snap CoverageSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if snap.Version != "1" {
		t.Errorf("version = %q", snap.Version)
	}
	if snap.Threshold != 0.5 {
		t.Errorf("threshold = %v", snap.Threshold)
	}
	if snap.Totals.Passed != 7 {
		t.Errorf("totals = %+v", snap.Totals)
	}
	if got := snap.Files["a.test"]; got.Passed != 2 || got.Failed != 1 {
		t.Errorf("a.test = %+v", got)
	}
	if _, ok := snap.Files["b.test"]; !ok {
		t.Errorf("b.test missing")
	}
}

func TestCoverageSnapshot_BaselineRegression(t *testing.T) {
	baseline := &CoverageSnapshot{
		Files: map[string]CoverageEntry{
			"a.test": {Total: 3, Passed: 3},
			"b.test": {Total: 2, Passed: 2},
		},
	}
	current := &CoverageSnapshot{
		Files: map[string]CoverageEntry{
			"a.test": {Total: 3, Passed: 2},
			"b.test": {Total: 2, Passed: 2},
			"c.test": {Total: 1, Passed: 1},
		},
	}
	drops := compareToBaseline(baseline, current)
	if len(drops) != 1 || drops[0] != "a.test" {
		t.Errorf("drops = %v, want [a.test]", drops)
	}
}

func TestCoverageSnapshot_LoadMissingBaseline(t *testing.T) {
	dir := t.TempDir()
	snap, err := loadBaseline(filepath.Join(dir, "nope.json"))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if snap != nil {
		t.Errorf("expected nil baseline for missing file, got %+v", snap)
	}
}

func TestCoverageSnapshot_JSONIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	files := []fileStat{
		{path: "z.test", stats: Stats{Total: 1, Passed: 1}},
		{path: "a.test", stats: Stats{Total: 1, Passed: 1}},
	}
	total := Stats{Total: 2, Passed: 2}
	path := filepath.Join(dir, "cov.json")
	if err := writeCoverageSnapshot(path, files, total, 0); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	ai := strings.Index(text, `"a.test"`)
	zi := strings.Index(text, `"z.test"`)
	if ai < 0 || zi < 0 {
		t.Fatalf("missing entries: %s", text)
	}
	if ai > zi {
		t.Errorf("a.test should come before z.test in sorted output:\n%s", text)
	}
}
