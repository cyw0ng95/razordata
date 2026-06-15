package lg

import (
	"log/slog"
	"testing"
)

func BenchmarkLogWithRotation(b *testing.B) {
	dir := b.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "bench.log",
		MaxSize:  10 * 1024 * 1024, // 10MB
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		log.Info("benchmark", "iteration", i)
	}
}

func BenchmarkLogRotationTrigger(b *testing.B) {
	dir := b.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "bench.log",
		MaxSize:  1024, // 1KB - triggers often
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		log.Info("benchmark message with some content")
	}
}
