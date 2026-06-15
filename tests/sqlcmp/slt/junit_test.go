//go:build slt_corpus

package slt

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteJUnit_RoundTrip(t *testing.T) {
	files := []fileStat{
		{path: "select1.test", stats: Stats{Total: 10, Passed: 8, Failed: 1, Skipped: 1}},
		{path: "select2.test", stats: Stats{Total: 5, Passed: 5, Failed: 0}},
	}
	total := Stats{Total: 15, Passed: 13, Failed: 1, Skipped: 1, Duration: Duration(1234)}
	suite := NewJUnitSuite(files, total)
	dir := t.TempDir()
	out := filepath.Join(dir, "junit.xml")
	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if err := WriteJUnit(f, suite); err != nil {
		t.Fatalf("WriteJUnit: %v", err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("junit empty")
	}
	// Spot-check: the file should contain the file paths.
	data, _ := os.ReadFile(out)
	for _, expect := range []string{`name="slt"`, `select1.test`, `select2.test`} {
		if !containsBytes(data, expect) {
			t.Errorf("expected %q in junit output", expect)
		}
	}
}

func containsBytes(haystack []byte, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
