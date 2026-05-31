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

// TestSync verifies Sync flushes the handler.
func TestSync(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Format: "text", Output: &buf})
	log.Info("sync test")

	if err := log.Sync(); err != nil {
		t.Errorf("Sync() returned error: %v", err)
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

// TestInterfaceCompliance verifies logger implements Logger interface.
func TestInterfaceCompliance(t *testing.T) {
	var _ Logger = (*logger)(nil)
}
