//go:build !debug

package EC

type AssertContext struct{}

type AssertCase func(AssertContext)

func WARN_ON(cond bool, msg string, args ...any) {}

func BUG_ON(cond bool, msg string, args ...any) {}

func PANIC_ON(cond bool, msg string, args ...any) {}

func RegisterAssertCase(fn AssertCase) {}

func RegisterBuiltinAssertCases() {}

func SetEngineStatsCallback(fn func() string) {}

func SetActiveTxnsCallback(fn func() string) {}
