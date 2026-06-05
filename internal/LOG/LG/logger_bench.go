package lg

import (
	"io"
	"log/slog"
	"sync"
	"testing"
)

// BenchmarkConcurrentLog measures concurrent logging throughput.
func BenchmarkConcurrentLog(b *testing.B) {
	log := New(Options{Level: slog.LevelDebug, Format: "text", Output: io.Discard})

	var wg sync.WaitGroup
	goroutines := 100
	callsPerGoroutine := b.N / goroutines
	if callsPerGoroutine == 0 {
		callsPerGoroutine = 1
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < callsPerGoroutine; j++ {
				log.Info("benchmark", "i", i, "j", j)
			}
		}()
	}

	wg.Wait()
}

// BenchmarkLogDisabled measures overhead when level is disabled.
func BenchmarkLogDisabled(b *testing.B) {
	log := New(Options{Level: slog.LevelError, Format: "text", Output: io.Discard})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		log.Info("disabled", "n", i)
	}
}

// BenchmarkLogEnabled measures throughput when level is enabled.
func BenchmarkLogEnabled(b *testing.B) {
	log := New(Options{Level: slog.LevelDebug, Format: "text", Output: io.Discard})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		log.Info("enabled", "n", i)
	}
}

// BenchmarkLogWithArgs measures overhead of With().
func BenchmarkLogWithArgs(b *testing.B) {
	log := New(Options{Level: slog.LevelDebug, Format: "text", Output: io.Discard})
	child := log.With("component", "benchmark", "version", "1.0")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		child.Info("message", "n", i)
	}
}

// dummy import for io
var _ = io.Discard
