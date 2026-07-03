//go:build debug

package sk

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/cyw0ng95/razordata/internal/DBG/CT"
	"github.com/cyw0ng95/razordata/internal/DBG/PR"
)

// Dispatch parses a command line and returns the response.
func Dispatch(line string) string {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return "ERROR empty command"
	}

	cmd := strings.ToLower(parts[0])
	switch cmd {
	case "heap":
		path, err := pr.DumpProfile("heap", 0)
		if err != nil {
			return fmt.Sprintf("ERROR %v", err)
		}
		return fmt.Sprintf("OK heap profile written to %s", path)
	case "cpu":
		dur := 1
		if len(parts) > 1 {
			fmt.Sscanf(parts[1], "%d", &dur)
		}
		path, err := pr.DumpProfile("cpu", time.Duration(dur)*time.Second)
		if err != nil {
			return fmt.Sprintf("ERROR %v", err)
		}
		return fmt.Sprintf("OK cpu profile written to %s", path)
	case "goroutine":
		path, err := pr.DumpProfile("goroutine", 0)
		if err != nil {
			return fmt.Sprintf("ERROR %v", err)
		}
		return fmt.Sprintf("OK goroutine profile written to %s", path)
	case "stats":
		snap := ct.GlobalStats.Snapshot()
		var b strings.Builder
		for k, v := range snap {
			fmt.Fprintf(&b, "%s: %d\n", k, v)
		}
		return b.String()
	case "gc":
		runtime.GC()
		return "OK gc performed"
	case "help":
		return "commands: heap, cpu N, goroutine, stats, gc, help"
	default:
		return fmt.Sprintf("ERROR unknown command: %s", cmd)
	}
}
