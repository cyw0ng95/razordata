//go:build debug

package EC

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/cyw0ng95/razordata/internal/LOG/HK"
)

type AssertContext struct {
	Caller    string
	Message   string
	Stack     string
	StartedAt time.Time
}

type AssertCase func(AssertContext)

var assertCases []AssertCase

var engineStatsFn func() string
var activeTxnsFn func() string

func callerOfAssert(skip int) string {
	pc, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "???"
	}
	fn := runtime.FuncForPC(pc)
	return fmt.Sprintf("%s:%d %s", file, line, fn.Name())
}

func buildCtx(msg string) AssertContext {
	return AssertContext{
		Caller:    callerOfAssert(3),
		Message:   msg,
		Stack:     string(debug.Stack()),
		StartedAt: time.Now(),
	}
}

func runCases(ctx AssertContext) {
	for _, fn := range assertCases {
		fn(ctx)
	}
}

func WARN_ON(cond bool, msg string, args ...any) {
	if !cond {
		return
	}
	formatted := fmt.Sprintf(msg, args...)
	ctx := buildCtx(formatted)
	runCases(ctx)
	slog.Warn("WARN_ON: " + formatted)
}

func BUG_ON(cond bool, msg string, args ...any) {
	if !cond {
		return
	}
	formatted := fmt.Sprintf(msg, args...)
	ctx := buildCtx(formatted)
	runCases(ctx)
	slog.Error("BUG_ON: " + formatted)
	os.Exit(1)
}

func PANIC_ON(cond bool, msg string, args ...any) {
	if !cond {
		return
	}
	formatted := fmt.Sprintf(msg, args...)
	ctx := buildCtx(formatted)
	runCases(ctx)
	panic(formatted)
}

func RegisterAssertCase(fn AssertCase) {
	assertCases = append(assertCases, fn)
}

func SetEngineStatsCallback(fn func() string) {
	engineStatsFn = fn
}

func SetActiveTxnsCallback(fn func() string) {
	activeTxnsFn = fn
}

func case_01_stack(ctx AssertContext) {
	os.Stderr.WriteString("--- [ASSERT stack] ---\n")
	os.Stderr.WriteString(ctx.Stack)
	os.Stderr.WriteString("--- [ASSERT stack end] ---\n")
}

func case_02_trace_flush(ctx AssertContext) {
	if hk.DefaultSink == nil {
		return
	}
	stats := hk.DefaultSink.Stats()
	fmt.Fprintf(os.Stderr, "--- [ASSERT trace] Capacity=%d Used=%d Dropped=%d ---\n",
		stats.Capacity, stats.Used, stats.Dropped)
}

func case_03_counter_snapshot(ctx AssertContext) {
	if hk.DefaultMetricSink == nil {
		return
	}
	snap := hk.DefaultMetricSink.Snapshot()
	os.Stderr.WriteString("--- [ASSERT counters] ---\n")
	for k, v := range snap.Counters {
		fmt.Fprintf(os.Stderr, "  %s = %d\n", k, v)
	}
}

func case_04_engine_stats(ctx AssertContext) {
	if engineStatsFn == nil {
		return
	}
	s := engineStatsFn()
	os.Stderr.WriteString("--- [ASSERT engine stats] ---\n")
	os.Stderr.WriteString(s)
	os.Stderr.WriteString("\n")
}

func case_05_active_txns(ctx AssertContext) {
	if activeTxnsFn == nil {
		return
	}
	s := activeTxnsFn()
	os.Stderr.WriteString("--- [ASSERT active txns] ---\n")
	os.Stderr.WriteString(s)
	os.Stderr.WriteString("\n")
}

func RegisterBuiltinAssertCases() {
	RegisterAssertCase(case_01_stack)
	RegisterAssertCase(case_02_trace_flush)
	RegisterAssertCase(case_03_counter_snapshot)
	RegisterAssertCase(case_04_engine_stats)
	RegisterAssertCase(case_05_active_txns)
}
