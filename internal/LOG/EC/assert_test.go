//go:build debug

package EC

import (
	"os"
	"os/exec"
	"testing"
)

func TestWARN_ON_NoOpWhenFalse(t *testing.T) {
	WARN_ON(false, "should not fire")
	BUG_ON(false, "should not fire")
	PANIC_ON(false, "should not fire")
}

func TestPANIC_ON_FiresWhenTrue(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic from PANIC_ON(true)")
		}
	}()
	PANIC_ON(true, "test panic")
}

func TestBUG_ON_FiresWhenTrue(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		BUG_ON(true, "test bug")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestBUG_ON_FiresWhenTrue")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatal("expected BUG_ON to exit(1), but it didn't")
}

func TestCasesChain_RunsRegistered(t *testing.T) {
	assertCases = nil
	var ran bool
	RegisterAssertCase(func(ctx AssertContext) {
		ran = true
	})
	WARN_ON(true, "test chain")
	if !ran {
		t.Error("expected registered case to run")
	}
}

func TestCasesChain_Multiple(t *testing.T) {
	assertCases = nil
	var count int
	RegisterAssertCase(func(ctx AssertContext) {
		count++
	})
	RegisterAssertCase(func(ctx AssertContext) {
		count++
	})
	WARN_ON(true, "test multi")
	if count != 2 {
		t.Errorf("expected both cases to run, got count=%d", count)
	}
}

func TestRegisterBuiltinAssertCases_RegistersFive(t *testing.T) {
	assertCases = nil
	RegisterBuiltinAssertCases()
	if len(assertCases) != 5 {
		t.Errorf("expected 5 built-in cases, got %d", len(assertCases))
	}
}

func TestSetCallbacks(t *testing.T) {
	var called bool
	SetEngineStatsCallback(func() string {
		called = true
		return "engine stats"
	})
	SetActiveTxnsCallback(func() string {
		called = true
		return "active txns"
	})
	if !called {
		t.Log("callbacks not invoked without assertion")
	}
}

func TestWARN_ON_FormatsArgs(t *testing.T) {
	var msg string
	assertCases = nil
	RegisterAssertCase(func(ctx AssertContext) {
		msg = ctx.Message
	})
	WARN_ON(true, "hello %s", "world")
	if msg != "hello world" {
		t.Errorf("expected 'hello world', got %q", msg)
	}
}

func TestPANIC_ON_Message(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if r != "panic msg" {
			t.Errorf("expected 'panic msg', got %v", r)
		}
	}()
	PANIC_ON(true, "panic msg")
}

func TestContext_HasCaller(t *testing.T) {
	var ctx AssertContext
	assertCases = nil
	RegisterAssertCase(func(c AssertContext) {
		ctx = c
	})
	WARN_ON(true, "caller test")
	if ctx.Caller == "" {
		t.Error("expected non-empty Caller")
	}
	if ctx.Stack == "" {
		t.Error("expected non-empty Stack")
	}
	if ctx.Message != "caller test" {
		t.Errorf("expected 'caller test', got %q", ctx.Message)
	}
}
