package AD

import (
	"log/slog"
	"sync/atomic"
)

// AdqcMetrics holds optional telemetry counters for adaptive compilation.
type AdqcMetrics struct {
	SpecializedTotal  atomic.Int64
	FallbackTotal     atomic.Int64
	InvalidationTotal atomic.Int64
}

var globalAdqcMetrics AdqcMetrics

// AdqcMetricsPtr returns a pointer to the global ADQC metrics.
func AdqcMetricsPtr() *AdqcMetrics {
	return &globalAdqcMetrics
}

// emitAdqcSpecialized logs a successful specialization event.
func emitAdqcSpecialized(planHash string, schemaVersion uint64, compileDurationNs, savedNsPerRow int64) {
	globalAdqcMetrics.SpecializedTotal.Add(1)
	slog.Debug("adqc: plan specialized",
		"planHash", planHash,
		"schemaVersion", schemaVersion,
		"compileDurationNs", compileDurationNs,
		"savedNsPerRow", savedNsPerRow,
	)
}

// emitAdqcFallback logs a fallback event (specialization not possible).
func emitAdqcFallback(planHash, reason string) {
	globalAdqcMetrics.FallbackTotal.Add(1)
	slog.Debug("adqc: fallback",
		"planHash", planHash,
		"reason", reason,
	)
}

// emitAdqcInvalidation logs a cache invalidation event.
func emitAdqcInvalidation(planHash string, trigger string) {
	globalAdqcMetrics.InvalidationTotal.Add(1)
	slog.Debug("adqc: invalidation",
		"planHash", planHash,
		"trigger", trigger,
	)
}

// init ensures the global metrics struct is zero-initialized at package load.
func init() {
	_ = globalAdqcMetrics
}
