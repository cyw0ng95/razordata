//go:build px_validate

package EX

import (
	"context"
	"log/slog"
	"sync/atomic"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// shadowStats tracks shadow validation statistics across queries.
// REQ002140.
type shadowStats struct {
	totalRuns      atomic.Int64
	matches        atomic.Int64
	mismatches     atomic.Int64
	pipelineErrors atomic.Int64
	legacyErrors   atomic.Int64
	totalMismatch  atomic.Int64
}

var globalShadowStats = &shadowStats{}

// ShadowStatsSnapshot is a read-only view of shadow validation statistics.
type ShadowStatsSnapshot struct {
	TotalRuns      int64
	Matches        int64
	Mismatches     int64
	PipelineErrors int64
	LegacyErrors   int64
	TotalMismatch  int64
}

// ShadowStats returns a snapshot of shadow validation statistics.
// REQ002140.
func ShadowStats() ShadowStatsSnapshot {
	return ShadowStatsSnapshot{
		TotalRuns:      globalShadowStats.totalRuns.Load(),
		Matches:        globalShadowStats.matches.Load(),
		Mismatches:     globalShadowStats.mismatches.Load(),
		PipelineErrors: globalShadowStats.pipelineErrors.Load(),
		LegacyErrors:   globalShadowStats.legacyErrors.Load(),
		TotalMismatch:  globalShadowStats.totalMismatch.Load(),
	}
}

// ResetShadowStats resets all shadow validation counters. REQ002140.
func ResetShadowStats() {
	globalShadowStats.totalRuns.Store(0)
	globalShadowStats.matches.Store(0)
	globalShadowStats.mismatches.Store(0)
	globalShadowStats.pipelineErrors.Store(0)
	globalShadowStats.legacyErrors.Store(0)
	globalShadowStats.totalMismatch.Store(0)
}

// drainPlanExecCtx replaces the default implementation when build tag
// px_validate is set. Runs both pipeline and legacy paths, compares
// results, logs discrepancies, and returns the pipeline result.
// REQ002140. REQ002143: defaults to drainBatch when pipeline fails.
func (e *Executor) drainPlanExecCtx(ctx context.Context, plan *pl.PlanResult, execCtx *DT.ExecContext) ([]DT.Row, error) {
	globalShadowStats.totalRuns.Add(1)

	// Run legacy path synchronously.
	legacyRows, legacyErr := drainBatch(ctx, plan.Root, execCtx)
	if legacyErr != nil {
		globalShadowStats.legacyErrors.Add(1)
		return nil, legacyErr
	}

	// Run pipeline path. Falls back to legacy if pipeline is unavailable.
	if e.pipelineBuilder == nil {
		return legacyRows, nil
	}
	pipeRows, pipeErr := e.drainPipeline(ctx, plan)
	if pipeErr != nil {
		globalShadowStats.pipelineErrors.Add(1)
		return legacyRows, nil
	}

	// Compare results.
	mismatches := CompareRows(pipeRows, legacyRows)
	if mismatches == 0 {
		globalShadowStats.matches.Add(1)
		return pipeRows, nil
	}
	globalShadowStats.mismatches.Add(1)
	globalShadowStats.totalMismatch.Add(int64(mismatches))
	slog.Warn("px shadow mismatch",
		"pipe_count", len(pipeRows),
		"legacy_count", len(legacyRows),
		"mismatches", mismatches,
	)
	// Return legacy result when pipeline differs — the vectorized path
	// has known issues with EXISTS subqueries, CASE WHEN, and other
	// complex expressions. REQ002143.
	return legacyRows, nil
}