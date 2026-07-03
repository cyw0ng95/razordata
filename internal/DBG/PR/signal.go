//go:build debug

package pr

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

// InstallSignalHandlers registers SIGUSR1 (heap+goroutine) and SIGUSR2 (CPU 5s).
func InstallSignalHandlers() {
	sigUsr1 := make(chan os.Signal, 1)
	sigUsr2 := make(chan os.Signal, 1)
	signal.Notify(sigUsr1, syscall.SIGUSR1)
	signal.Notify(sigUsr2, syscall.SIGUSR2)

	go func() {
		for range sigUsr1 {
			DumpProfile("heap", 0)
			DumpProfile("goroutine", 0)
		}
	}()
	go func() {
		for range sigUsr2 {
			DumpProfile("cpu", 5*time.Second)
		}
	}()
}
