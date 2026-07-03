//go:build debug

package sk

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	jd "github.com/cyw0ng95/razordata/internal/DBG/JD"
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
	case "debug_join":
		if len(parts) < 2 {
			return "usage: debug_join on|off|summary|detailed|full"
		}
		level := strings.ToLower(parts[1])
		switch level {
		case "off":
			jd.SetJoinTracer(nil)
			jd.SetVerbosity(jd.LevelOff)
			return "OK debug_join tracing disabled"
		case "on", "summary":
			jd.SetJoinTracer(jd.NewBufferedTracer(4096, jd.LevelSummary))
			jd.SetVerbosity(jd.LevelSummary)
			return "OK debug_join level=summary"
		case "detailed":
			jd.SetJoinTracer(jd.NewBufferedTracer(4096, jd.LevelDetailed))
			jd.SetVerbosity(jd.LevelDetailed)
			return "OK debug_join level=detailed"
		case "full":
			jd.SetJoinTracer(jd.NewBufferedTracer(4096, jd.LevelFull))
			jd.SetVerbosity(jd.LevelFull)
			return "OK debug_join level=full"
		default:
			return fmt.Sprintf("ERROR unknown level: %s", level)
		}
	case "debug_join_flush":
		tr := jd.GetJoinTracer()
		if tr == nil {
			return "no active join tracer"
		}
		buffered, ok := tr.(*jd.BufferedTracer)
		if !ok {
			return "tracer does not support flush"
		}
		events := buffered.Flush()
		var b strings.Builder
		for _, e := range events {
			fmt.Fprintf(&b, "%s\n", e.String())
		}
		return b.String()
	case "debug_join_filter":
		if len(parts) < 2 {
			return "usage: debug_join_filter <operator>"
		}
		tr := jd.GetJoinTracer()
		if tr == nil {
			return "no active join tracer"
		}
		buffered, ok := tr.(*jd.BufferedTracer)
		if !ok {
			return "tracer does not support filter"
		}
		events := buffered.Flush()
		filterOp := parts[1]
		var b strings.Builder
		count := 0
		for _, e := range events {
			if strings.EqualFold(e.Op, filterOp) {
				fmt.Fprintf(&b, "%s\n", e.String())
				count++
			}
		}
		fmt.Fprintf(&b, "--- %d events matching %s ---\n", count, filterOp)
		return b.String()
	case "debug_join_summary":
		tr := jd.GetJoinTracer()
		if tr == nil {
			return "no active join tracer"
		}
		buffered, ok := tr.(*jd.BufferedTracer)
		if !ok {
			return "tracer does not support summary"
		}
		events := buffered.Flush()
		operatorRows := map[string]int64{}
		predicatePass := map[string]int64{}
		predicateFail := map[string]int64{}
		columnMismatches := 0
		for _, e := range events {
			switch e.Type {
			case jd.EventRowFlow:
				if !e.Entering {
					operatorRows[e.Op]++
				}
			case jd.EventPredicate:
				if e.Passed {
					predicatePass[e.Op]++
				} else {
					predicateFail[e.Op]++
				}
			case jd.EventColumnOffset:
				if e.ExpectedCols != e.ActualCols {
					columnMismatches++
				}
			}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "=== Join Debug Summary ===\n")
		fmt.Fprintf(&b, "Rows per operator:\n")
		for op, rows := range operatorRows {
			fmt.Fprintf(&b, "  %s: %d\n", op, rows)
		}
		fmt.Fprintf(&b, "Predicate results:\n")
		for op := range predicatePass {
			fmt.Fprintf(&b, "  %s: %d pass, %d fail\n", op, predicatePass[op], predicateFail[op])
		}
		fmt.Fprintf(&b, "Column offset mismatches: %d\n", columnMismatches)
		return b.String()
	case "help":
		return "commands: heap, cpu N, goroutine, stats, gc, debug_join on|off|summary|detailed|full, debug_join_flush, debug_join_filter <op>, debug_join_summary, help"
	default:
		return fmt.Sprintf("ERROR unknown command: %s", cmd)
	}
}
