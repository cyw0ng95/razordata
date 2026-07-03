//go:build debug

package UT

import (
	"fmt"
	"strings"

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
	default:
		return "", fmt.Errorf("unknown debug pragma: %s", pragma)
	}
}
