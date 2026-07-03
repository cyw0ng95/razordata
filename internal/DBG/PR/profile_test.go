//go:build debug

package pr

import (
	"os"
	"testing"
	"time"
)

func TestDumpProfile_Heap(t *testing.T) {
	dir := t.TempDir()
	SetProfileDir(dir)
	path, err := DumpProfile("heap", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("profile file not created: %v", err)
	}
	if info.Size() == 0 {
		t.Error("profile file is empty")
	}
}

func TestDumpProfile_CPU(t *testing.T) {
	dir := t.TempDir()
	SetProfileDir(dir)
	path, err := DumpProfile("cpu", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("profile file not created: %v", err)
	}
}

func TestDumpProfile_UnknownKind(t *testing.T) {
	dir := t.TempDir()
	SetProfileDir(dir)
	_, err := DumpProfile("nonexistent", 0)
	if err == nil {
		t.Error("expected error for unknown profile kind")
	}
}

func TestDumpProfile_Goroutine(t *testing.T) {
	dir := t.TempDir()
	SetProfileDir(dir)
	path, err := DumpProfile("goroutine", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("profile file not created: %v", err)
	}
}
