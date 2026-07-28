//go:build px_validate

package EX

// This file is intentionally empty. The shadow validation implementation
// lives in drain_batch_shadow.go (same build tag) which defines
// drainPlanExecCtx to run both paths in parallel and compare results.
// REQ002140.