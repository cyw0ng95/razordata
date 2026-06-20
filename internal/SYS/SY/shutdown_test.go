package SY_test

import (
	"context"
	"testing"
	"time"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SY"
)

func TestInstallSignalHandler_DoubleStop(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	eng, err := SY.Open(ctx, dir, AP.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close(ctx)

	stop := SY.InstallSignalHandler(ctx, eng)
	stop()
	stop()
}

func TestInstallSignalHandler_ContextCancel(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	eng, err := SY.Open(ctx, dir, AP.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close(ctx)

	cancelCtx, cancel := context.WithCancel(ctx)
	stop := SY.InstallSignalHandler(cancelCtx, eng)
	defer stop()

	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestInstallSignalHandler_ReturnsStopFunc(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	eng, err := SY.Open(ctx, dir, AP.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close(ctx)

	stop := SY.InstallSignalHandler(ctx, eng)
	if stop == nil {
		t.Fatal("expected non-nil stop function")
	}
	stop()
}
