//go:build debug

package UT

import (
	"fmt"
	"strings"

	cd "github.com/cyw0ng95/razordata/internal/DBG/CD"
	jd "github.com/cyw0ng95/razordata/internal/DBG/JD"
	"github.com/cyw0ng95/razordata/internal/DBG/CT"
	"github.com/cyw0ng95/razordata/internal/DBG/IN"
)

// HandleDebugPragma dispatches debug-specific PRAGMA commands.
func HandleDebugPragma(pragma string, args []string) (string, error) {
	switch strings.ToLower(pragma) {
	case "buffer_pool":
		pages := in.BufferPool()
		var b strings.Builder
		for _, p := range pages {
			fmt.Fprintf(&b, "%s:%d pinned=%v dirty=%v\n", p.Segment, p.BlockNum, p.Pinned, p.Dirty)
		}
		return b.String(), nil
	case "active_txns":
		txns := in.ActiveTxns()
		var b strings.Builder
		for _, tx := range txns {
			fmt.Fprintf(&b, "id=%d state=%s readonly=%v\n", tx.ID, tx.State, tx.ReadOnly)
		}
		return b.String(), nil
	case "dump_page":
		if len(args) < 2 {
			return "", fmt.Errorf("usage: PRAGMA dump_page(segment, blockNum)")
		}
		var blockNum uint32
		fmt.Sscanf(args[1], "%d", &blockNum)
		dump, err := in.InspectPage(args[0], blockNum)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("page %s:%d type=%d items=%d free=%d checksum=%d",
			dump.Segment, dump.BlockNum, dump.PageType, dump.NumItems, dump.FreeOffset, dump.Checksum), nil
	case "query_stats", "engine_counters", "wal_stats":
		snap := ct.GlobalStats.Snapshot()
		var b strings.Builder
		for k, v := range snap {
			fmt.Fprintf(&b, "%s: %d\n", k, v)
		}
		return b.String(), nil
	case "debug_join_tracing":
		if len(args) == 0 {
			return fmt.Sprintf("debug_join_tracing verbosity=%d", jd.GetVerbosity()), nil
		}
		switch strings.ToLower(args[0]) {
		case "off", "0":
			jd.SetJoinTracer(nil)
			jd.SetVerbosity(jd.LevelOff)
			return "OK debug_join_tracing disabled", nil
		case "summary", "1":
			jd.SetJoinTracer(jd.NewBufferedTracer(4096, jd.LevelSummary))
			jd.SetVerbosity(jd.LevelSummary)
			return "OK debug_join_tracing level=summary", nil
		case "detailed", "2":
			jd.SetJoinTracer(jd.NewBufferedTracer(4096, jd.LevelDetailed))
			jd.SetVerbosity(jd.LevelDetailed)
			return "OK debug_join_tracing level=detailed", nil
		case "full", "3":
			jd.SetJoinTracer(jd.NewBufferedTracer(4096, jd.LevelFull))
			jd.SetVerbosity(jd.LevelFull)
			return "OK debug_join_tracing level=full", nil
		default:
			return "", fmt.Errorf("unknown verbosity level: %s", args[0])
		}
case "debug_join_flush":
		tr := jd.GetJoinTracer()
		if tr == nil {
			return "no active join tracer", nil
		}
		buffered, ok := tr.(*jd.BufferedTracer)
		if !ok {
			return "tracer does not support flush", nil
		}
		events := buffered.Flush()
		var b strings.Builder
		for _, e := range events {
			fmt.Fprintf(&b, "%s\n", e.String())
		}
		return b.String(), nil
	case "debug_join_filter":
		if len(args) == 0 {
			return "usage: debug_join_filter(operator=<name>)", nil
		}
		tr := jd.GetJoinTracer()
		if tr == nil {
			return "no active join tracer", nil
		}
		buffered, ok := tr.(*jd.BufferedTracer)
		if !ok {
			return "tracer does not support filter", nil
		}
		events := buffered.Flush()
		filterOp := strings.TrimPrefix(args[0], "operator=")
		var b strings.Builder
		count := 0
		for _, e := range events {
			if filterOp == "" || strings.EqualFold(e.Op, filterOp) {
				fmt.Fprintf(&b, "%s\n", e.String())
				count++
			}
		}
		fmt.Fprintf(&b, "--- %d events matching filter ---\n", count)
		return b.String(), nil
	case "debug_join_summary":
		tr := jd.GetJoinTracer()
		if tr == nil {
			return "no active join tracer", nil
		}
		buffered, ok := tr.(*jd.BufferedTracer)
		if !ok {
			return "tracer does not support summary", nil
		}
		events := buffered.Flush()
		// Aggregate stats
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
		return b.String(), nil
	case "debug_cte_tracing":
		if len(args) == 0 {
			return fmt.Sprintf("debug_cte_tracing verbosity=%d", cd.GetVerbosity()), nil
		}
		switch strings.ToLower(args[0]) {
		case "off", "0":
			cd.SetCTETracer(nil)
			cd.SetVerbosity(cd.LevelOff)
			return "OK debug_cte_tracing disabled", nil
		case "on", "1", "summary":
			cd.SetCTETracer(cd.NewBufferedTracer(4096, cd.LevelSummary))
			cd.SetVerbosity(cd.LevelSummary)
			return "OK debug_cte_tracing level=summary", nil
		case "detailed", "2":
			cd.SetCTETracer(cd.NewBufferedTracer(4096, cd.LevelDetailed))
			cd.SetVerbosity(cd.LevelDetailed)
			return "OK debug_cte_tracing level=detailed", nil
		case "full", "3":
			cd.SetCTETracer(cd.NewBufferedTracer(4096, cd.LevelFull))
			cd.SetVerbosity(cd.LevelFull)
			return "OK debug_cte_tracing level=full", nil
		default:
			return "", fmt.Errorf("unknown CTE tracing verbosity: %s", args[0])
		}
	case "debug_cte_flush":
		tr := cd.GetCTETracer()
		if tr == nil {
			return "no active CTE tracer", nil
		}
		buffered, ok := tr.(*cd.BufferedTracer)
		if !ok {
			return "tracer does not support flush", nil
		}
		events := buffered.Flush()
		var b strings.Builder
		for _, e := range events {
			fmt.Fprintf(&b, "%s\n", e.String())
		}
		return b.String(), nil
	case "debug_cte_summary":
		tr := cd.GetCTETracer()
		if tr == nil {
			return "no active CTE tracer", nil
		}
		buffered, ok := tr.(*cd.BufferedTracer)
		if !ok {
			return "tracer does not support summary", nil
		}
		events := buffered.Flush()
		seedRows := map[string]int64{}
		iterations := map[string]int64{}
		armRows := map[string]int64{}
		maxReached := map[string]bool{}
		for _, e := range events {
			switch e.Type {
			case cd.EventSeed:
				seedRows[e.Name] += int64(e.NumRows)
			case cd.EventIteration:
				iterations[e.Name]++
				armRows[e.Name] += int64(e.RowsOut)
			case cd.EventMaxIterations:
				maxReached[e.Name] = true
			}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "=== CTE Debug Summary ===\n")
		for name := range seedRows {
			fmt.Fprintf(&b, "CTE %q:\n", name)
			fmt.Fprintf(&b, "  seed rows: %d\n", seedRows[name])
			fmt.Fprintf(&b, "  iterations: %d\n", iterations[name])
			fmt.Fprintf(&b, "  arm rows produced: %d\n", armRows[name])
			if maxReached[name] {
				fmt.Fprintf(&b, "  max iterations reached: yes\n")
			}
		}
		if len(seedRows) == 0 {
			fmt.Fprintf(&b, "  (no CTE events captured)\n")
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("unknown debug pragma: %s", pragma)
	}
}