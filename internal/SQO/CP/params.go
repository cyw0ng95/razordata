// Package CP owns the CostParams struct and calibration. Single source
// of truth for cost coefficients. Two operating modes: legacy
// (dimensionless units, PG-compatible) and absolute-time (target).
// REQ001473 / REQ001474.
package CP

import "time"

// CostParams is the cost-model parameter set. Two operating modes:
//
//   - Legacy (AbsoluteTime == false): dimensionless units, PG-compatible
//     defaults (SeqPageCost=1.0, RandomPageCost=4.0, etc.). Cannot be
//     auto-calibrated against actual wall-clock time.
//   - Absolute-time (AbsoluteTime == true): nanosecond-scale estimates
//     per operator. Target design — supports auto-calibration via
//     SQO/CL/calibrate.go micro-benchmarks against the live store.
//
// REQ001104, REQ001450, REQ001474.
type CostParams struct {
	// Legacy dimensionless units (PG-compatible defaults).
	SeqPageCost       float64
	RandomPageCost    float64
	CPUTupleCost      float64
	CPUIndexTupleCost float64
	CPUOperatorCost   float64

	// Absolute-time units (target).
	SeqScanNanos   time.Duration
	IndexNanos     time.Duration
	HashBuildNanos time.Duration
	HashProbeNanos time.Duration
	NLJOuterNanos  time.Duration
	SortNanos      time.Duration

	// Mode flag. When true, the *Nanos fields drive cost; when false,
	// the legacy float64 fields do.
	AbsoluteTime bool
}

// Default returns a CostParams with PG-compatible legacy defaults.
// REQ001104.
func Default() CostParams {
	return CostParams{
		SeqPageCost:       1.0,
		RandomPageCost:    4.0,
		CPUTupleCost:      0.01,
		CPUIndexTupleCost: 0.005,
		CPUOperatorCost:   0.0025,
	}
}
