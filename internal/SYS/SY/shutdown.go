package SY

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// InstallSignalHandler subscribes to SIGINT and SIGTERM and calls
// Close with the provided context on the first signal. A second
// signal aborts the wait and returns immediately. Returns a stop
// function that the caller invokes to unsubscribe and release the
// internal goroutine.
//
// The handler is best-effort: a test that doesn't want OS-level
// signals can call Engine.Close directly.
func InstallSignalHandler(ctx context.Context, e *Engine) (stop func()) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-sigCh:
			_ = e.Close(context.Background())
		}
	}()
	return func() {
		signal.Stop(sigCh)
		close(done)
	}
}
