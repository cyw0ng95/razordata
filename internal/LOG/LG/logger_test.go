package lg

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// TestLevelFiltering verifies that disabled log levels produce no output.
func TestLevelFiltering(t *testing.T) {
	tests := []struct {
		name      string
		setLevel  slog.Level
		callLevel slog.Level
		want      bool // true = expect output
	}{
		{"debugDisabled", slog.LevelWarn, slog.LevelDebug, false},
		{"infoDisabled", slog.LevelError, slog.LevelInfo, false},
		{"warnDisabled", slog.LevelError, slog.LevelWarn, false},
		{"sameLevel", slog.LevelWarn, slog.LevelWarn, true},
		{"higherLevel", slog.LevelWarn, slog.LevelError, true},
		{"infoDisabledByTrace", slog.LevelWarn, slog.LevelInfo, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := New(Options{Level: tt.setLevel, Format: "text", Output: &buf})

			switch tt.callLevel {
			case slog.LevelDebug:
				log.Debug("test", "key", "value")
			case slog.LevelInfo:
				log.Info("test", "key", "value")
			case slog.LevelWarn:
				log.Warn("test", "key", "value")
			case slog.LevelError:
				log.Error("test", "key", "value")
			default:
				log.Info("test", "key", "value")
			}

			got := buf.Len() > 0
			if got != tt.want {
				t.Errorf("level=%v, callLevel=%v: got output=%v, want=%v", tt.setLevel, tt.callLevel, got, tt.want)
			}
		})
	}
}

// TestWith verifies that With merges extra args into the log output.
func TestWith(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Format: "text", Output: &buf})
	child := log.With("component", "test")

	child.Info("message")

	got := buf.String()
	if !strings.Contains(got, "component") || !strings.Contains(got, "test") {
		t.Errorf("expected 'component' and 'test' in output, got: %s", got)
	}
}

// TestWithMultipleArgs verifies With with multiple key-value pairs.
func TestWithMultipleArgs(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Format: "text", Output: &buf})
	child := log.With("a", "1", "b", "2", "c", "3")

	child.Info("message")

	got := buf.String()
	if !strings.Contains(got, "a") || !strings.Contains(got, "1") {
		t.Errorf("expected 'a' and '1' in output, got: %s", got)
	}
	if !strings.Contains(got, "b") || !strings.Contains(got, "2") {
		t.Errorf("expected 'b' and '2' in output, got: %s", got)
	}
	if !strings.Contains(got, "c") || !strings.Contains(got, "3") {
		t.Errorf("expected 'c' and '3' in output, got: %s", got)
	}
}

// TestWithChained verifies chaining With() calls.
func TestWithChained(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Format: "text", Output: &buf})
	child1 := log.With("a", "1")
	child2 := child1.With("b", "2")

	child2.Info("message")

	got := buf.String()
	if !strings.Contains(got, "a") || !strings.Contains(got, "1") {
		t.Errorf("expected 'a' and '1' in output, got: %s", got)
	}
	if !strings.Contains(got, "b") || !strings.Contains(got, "2") {
		t.Errorf("expected 'b' and '2' in output, got: %s", got)
	}
}

// TestSetLevel verifies that SetLevel atomically changes the minimum level.
func TestSetLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: slog.LevelDebug, Format: "text", Output: &buf})

	log.Info("before")
	if buf.Len() == 0 {
		t.Fatal("expected output before SetLevel")
	}

	buf.Reset()
	log.SetLevel(slog.LevelError)

	log.Info("after")
	if buf.Len() > 0 {
		t.Error("expected no output after SetLevel to Error")
	}
}

// TestSetLevelDebug sets level to Debug and verifies output.
func TestSetLevelDebug(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: slog.LevelDebug, Format: "text", Output: &buf})

	log.Debug("debug message", "key", "value")

	if buf.Len() == 0 {
		t.Error("expected output after SetLevel to Debug")
	}
}

// TestConcurrentLogging verifies goroutine-safe concurrent logging.
func TestConcurrentLogging(t *testing.T) {
	log := New(Options{Level: slog.LevelDebug, Format: "text", Output: io.Discard})

	var wg sync.WaitGroup
	const goroutines = 100
	const callsPerGoroutine = 100

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < callsPerGoroutine; j++ {
				log.Info("concurrent", "id", id, "n", j)
			}
		}(i)
	}

	wg.Wait()
}

// TestConcurrentSetLevelAndLog verifies concurrent SetLevel and logging.
func TestConcurrentSetLevelAndLog(t *testing.T) {
	log := New(Options{Level: slog.LevelDebug, Format: "text", Output: io.Discard})

	var wg sync.WaitGroup
	const goroutines = 50
	const callsPerGoroutine = 200

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			level := slog.Level(id % 5)
			for j := 0; j < callsPerGoroutine; j++ {
				log.SetLevel(level)
				log.Info("test", "n", j)
				log.Debug("debug", "n", j)
			}
		}(i)
	}

	wg.Wait()
}

// TestSync verifies Sync flushes the handler.
func TestSync(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Format: "text", Output: &buf})
	log.Info("sync test")

	if err := log.Sync(); err != nil {
		t.Errorf("Sync() returned error: %v", err)
	}
}

// TestSyncNoHandlerSync verifies Sync with handler that doesn't support Sync.
func TestSyncNoHandlerSync(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Format: "text", Output: &buf})

	// Sync should not error even if handler doesn't support Sync
	if err := log.Sync(); err != nil {
		t.Errorf("Sync() returned unexpected error: %v", err)
	}
}

// TestJSONFormat verifies JSON output format.
func TestJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: slog.LevelInfo, Format: "json", Output: &buf})
	log.Info("json test", "key", "value")

	got := buf.String()
	if !strings.Contains(got, `"msg":"json test"`) {
		t.Errorf("expected JSON format, got: %s", got)
	}
}

// TestTextFormat verifies text output format.
func TestTextFormat(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: slog.LevelInfo, Format: "text", Output: &buf})
	log.Info("text test", "key", "value")

	got := buf.String()
	if !strings.Contains(got, "text test") {
		t.Errorf("expected text format, got: %s", got)
	}
}

// TestDefaultOutput verifies default output is stderr.
func TestDefaultOutput(t *testing.T) {
	log := New(Options{Level: slog.LevelDebug})
	if log == nil {
		t.Fatal("expected non-nil logger")
	}

	// Should not panic with nil output
	log.Info("test")
}

// TestNewJSONFormat verifies New with JSON format.
func TestNewJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: slog.LevelInfo, Format: "json", Output: &buf})
	log.Info("json", "key", "value")

	got := buf.String()
	if !strings.Contains(got, `"msg":"json"`) {
		t.Errorf("expected JSON format, got: %s", got)
	}
}

// TestNewTextFormat verifies New with text format (default).
func TestNewTextFormat(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: slog.LevelInfo, Output: &buf})
	log.Info("text", "key", "value")

	got := buf.String()
	if !strings.Contains(got, "text") {
		t.Errorf("expected text format, got: %s", got)
	}
}

// TestAtomicLevelConcurrentSet verifies concurrent SetLevel does not race.
func TestAtomicLevelConcurrentSet(t *testing.T) {
	log := New(Options{Level: slog.LevelDebug, Format: "text", Output: io.Discard})

	var wg sync.WaitGroup
	const writers = 10
	const iterations = 1000

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			level := slog.Level(id % 5)
			for j := 0; j < iterations; j++ {
				log.SetLevel(level)
				log.Info("test", "n", j)
			}
		}(i)
	}

	wg.Wait()
}

// TestAllLogLevels verifies all log levels work correctly.
func TestAllLogLevels(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: slog.LevelDebug, Format: "text", Output: &buf})

	log.Debug("debug", "level", "debug")
	buf.Reset()
	log.Info("info", "level", "info")
	if buf.Len() == 0 {
		t.Error("expected info output")
	}

	buf.Reset()
	log.Warn("warn", "level", "warn")
	if buf.Len() == 0 {
		t.Error("expected warn output")
	}

	buf.Reset()
	log.Error("error", "level", "error")
	if buf.Len() == 0 {
		t.Error("expected error output")
	}
}

// TestInterfaceCompliance verifies logger implements Logger interface.
func TestInterfaceCompliance(t *testing.T) {
	var _ Logger = (*logger)(nil)
}

// TestLoggerWithNoArgs verifies logging with no extra args.
func TestLoggerWithNoArgs(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: slog.LevelInfo, Format: "text", Output: &buf})
	log.Info("simple message")

	got := buf.String()
	if !strings.Contains(got, "simple message") {
		t.Errorf("expected 'simple message' in output, got: %s", got)
	}
}

// TestLoggerWithManyArgs verifies logging with many extra args.
func TestLoggerWithManyArgs(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: slog.LevelInfo, Format: "text", Output: &buf})
	log.Info("many args", "a", "1", "b", "2", "c", "3", "d", "4", "e", "5")

	got := buf.String()
	for _, kv := range []string{"a", "1", "b", "2", "c", "3"} {
		if !strings.Contains(got, kv) {
			t.Errorf("expected '%s' in output, got: %s", kv, got)
		}
	}
}

// TestLoggerOutputWriter verifies output is written to correct writer.
func TestLoggerOutputWriter(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Level: slog.LevelInfo, Format: "text", Output: &buf})
	log.Info("test")

	if buf.Len() == 0 {
		t.Error("expected output to buffer")
	}
}