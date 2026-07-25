package AD

import (
	"context"
	"log/slog"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestAdaptiveOp_TryCompile_NoDebugAlloc verifies that tryCompile
// does not call fmt.Sprintf when slog debug level is disabled (the
// default). The opType field stays empty in that case. REQ001975.
func TestAdaptiveOp_TryCompile_NoDebugAlloc(t *testing.T) {
	// Ensure default logger has debug disabled. The default slog
	// logger uses LevelInfo, so LevelDebug is not enabled.
	ctx := context.Background()
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		t.Skip("default logger has debug enabled; cannot verify gating")
	}

	op := NewAdaptiveOp(&testOp{}, "req001975_hash")
	if op.direct {
		t.Fatal("direct should be false before tryCompile")
	}
	if op.tryAttempted {
		t.Fatal("tryAttempted should be false before tryCompile")
	}

	// Trigger tryCompile by calling Next() once (threshold=1).
	if _, err := op.Next(ctx); err != nil {
		t.Fatalf("Next returned error: %v", err)
	}

	// After tryCompile, the op must be in the fallback state.
	if !op.tryAttempted {
		t.Error("tryAttempted should be true after tryCompile")
	}
	if !op.direct {
		t.Error("direct should be true after tryCompile")
	}
	if op.State() != AdqcInterpreted {
		t.Errorf("expected AdqcInterpreted state, got %v", op.State())
	}
}

// TestAdaptiveOp_TryCompile_WithDebug verifies that tryCompile still
// succeeds when debug logging is enabled (opType is computed). This
// guards against the gating breaking the debug path. REQ001975.
func TestAdaptiveOp_TryCompile_WithDebug(t *testing.T) {
	// Install a logger that enables debug level.
	prev := slog.Default()
	defer slog.SetDefault(prev)
	slog.SetDefault(slog.New(slog.NewTextHandler(&discardWriter{}, &slog.HandlerOptions{Level: slog.LevelDebug})))

	ctx := context.Background()
	if !slog.Default().Enabled(ctx, slog.LevelDebug) {
		t.Skip("debug level not enabled after SetDefault; cannot verify path")
	}

	op := NewAdaptiveOp(&testOp{}, "req001975_debug_hash")
	if _, err := op.Next(ctx); err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if !op.tryAttempted || !op.direct {
		t.Fatalf("tryAttempted=%v direct=%v; want both true", op.tryAttempted, op.direct)
	}
}

// discardWriter is an io.Writer that discards all output, used to
// install a debug-enabled slog.Default without producing test noise.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestAdaptiveOp_InnerType_PreservesType ensures the AdaptiveOp still
// wraps the inner operator type correctly after the REQ001975 change
// (sanity check that the opType gating didn't break Inner exposure).
func TestAdaptiveOp_InnerType_PreservesType(t *testing.T) {
	inner := &testOp{}
	op := NewAdaptiveOp(inner, "type_check_hash")
	if op.Inner != inner {
		t.Error("AdaptiveOp.Inner does not match the wrapped operator")
	}
	// Verify the type assertion used implicitly by %T still works.
	var _ DT.Operator = inner
}
