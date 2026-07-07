//go:build debug

package UT

import (
	"testing"
)

func TestCTETracePragma_OnOff(t *testing.T) {
	// Test enabling and disabling CTE tracing
	result, err := HandleDebugPragma("debug_cte_tracing", nil)
	if err != nil {
		t.Fatalf("get verbosity: %v", err)
	}
	if result == "" {
		t.Fatal("empty result")
	}

	result, err = HandleDebugPragma("debug_cte_tracing", []string{"on"})
	if err != nil {
		t.Fatalf("enable tracing: %v", err)
	}
	if result != "OK debug_cte_tracing level=summary" {
		t.Errorf("expected OK message, got %q", result)
	}

	result, err = HandleDebugPragma("debug_cte_tracing", []string{"off"})
	if err != nil {
		t.Fatalf("disable tracing: %v", err)
	}
	if result != "OK debug_cte_tracing disabled" {
		t.Errorf("expected OK message, got %q", result)
	}
}

func TestCTETracePragma_Flush(t *testing.T) {
	// Enable tracing
	HandleDebugPragma("debug_cte_tracing", []string{"on"})

	// Flush should return empty string when no events
	result, err := HandleDebugPragma("debug_cte_flush", nil)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	// Empty result is OK when no events captured

	// Disable tracing
	HandleDebugPragma("debug_cte_tracing", []string{"off"})

	_ = result
}

func TestCTETracePragma_Summary(t *testing.T) {
	// Enable tracing
	HandleDebugPragma("debug_cte_tracing", []string{"on"})

	// Summary should show no events
	result, err := HandleDebugPragma("debug_cte_summary", nil)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if result == "" {
		t.Fatal("empty summary result")
	}

	// Disable tracing
	HandleDebugPragma("debug_cte_tracing", []string{"off"})

	_ = result
}

func TestCTETracePragma_Verbosity(t *testing.T) {
	tests := []struct {
		level string
		want  string
	}{
		{"summary", "OK debug_cte_tracing level=summary"},
		{"detailed", "OK debug_cte_tracing level=detailed"},
		{"full", "OK debug_cte_tracing level=full"},
	}

	for _, tc := range tests {
		result, err := HandleDebugPragma("debug_cte_tracing", []string{tc.level})
		if err != nil {
			t.Errorf("level %s: %v", tc.level, err)
			continue
		}
		if result != tc.want {
			t.Errorf("level %s: got %q, want %q", tc.level, result, tc.want)
		}
	}

	// Reset
	HandleDebugPragma("debug_cte_tracing", []string{"off"})
}
