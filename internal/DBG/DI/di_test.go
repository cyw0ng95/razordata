//go:build debug

package di

import (
	"log/slog"
	"testing"
)

func TestNewDebugger_WiresClusters(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDebugger(Options{
		DebugDir:           dir,
		EnableDebugSocket:  false,
		EnableDebugSignals: false,
		TraceEventCapacity: 1024,
	})
	if err != nil {
		t.Fatalf("NewDebugger: %v", err)
	}
	defer d.Close()

	if d.Socket() != "" {
		t.Errorf("expected empty socket path, got %q", d.Socket())
	}

	if err := d.SetLogLevel("ENG", slog.LevelDebug); err != nil {
		t.Fatalf("SetLogLevel: %v", err)
	}

	if err := d.EnableTrace("sql", true); err != nil {
		t.Fatalf("EnableTrace: %v", err)
	}

	snap := d.Stats()
	if snap.QueriesTotal.Load() != 0 {
		t.Errorf("expected 0 queries, got %d", snap.QueriesTotal.Load())
	}
}

func TestDebugger_Close(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDebugger(Options{
		DebugDir:           dir,
		EnableDebugSocket:  false,
		EnableDebugSignals: false,
		TraceEventCapacity: 0,
	})
	if err != nil {
		t.Fatalf("NewDebugger: %v", err)
	}

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Double close should be safe
	if err := d.Close(); err != nil {
		t.Fatalf("Double close: %v", err)
	}
}

func TestDebugger_DumpProfile(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDebugger(Options{
		DebugDir:           dir,
		EnableDebugSocket:  false,
		EnableDebugSignals: false,
		TraceEventCapacity: 0,
	})
	if err != nil {
		t.Fatalf("NewDebugger: %v", err)
	}
	defer d.Close()

	path, err := d.DumpProfile("heap", 0)
	if err != nil {
		t.Fatalf("DumpProfile: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty path")
	}
}
