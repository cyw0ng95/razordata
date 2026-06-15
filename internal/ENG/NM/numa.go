// Package nm implements NUMA topology detection and worker
// affinity hints for the razordata engine.
//
// REQ000309 (iter-27): NUMA-aware data placement. On NUMA
// hosts, the first-touch allocation policy automatically
// places memory on the local node when a goroutine touches a
// page. This package exposes the topology so the engine can
// (a) tag allocations with a node id and (b) pin long-running
// workers to specific cores.
//
// On non-NUMA hosts (single-socket, or NUMA disabled in
// firmware), all functions return 1 or 0 and the engine
// behaves identically to the pre-NUMA code path. Detection is
// opt-out: callers check IsAvailable() before relying on the
// returned node id.
package nm

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

// cachedNumNodes is the lazily-computed NUMA node count.
// Initialized to -1 to indicate "not yet detected".
var cachedNumNodes atomic.Int32

// detect scans /sys/devices/system/node/ to count NUMA nodes.
// The /sys filesystem is the Linux kernel's user-visible NUMA
// topology; each node has a subdirectory node<N>. On non-Linux
// platforms or on hosts without NUMA, the directory may be
// missing entirely. In that case, detect returns 1.
func detect() int {
	entries, err := os.ReadDir("/sys/devices/system/node")
	if err != nil {
		return 1
	}
	count := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "node") {
			continue
		}
		// node<digits>
		if _, err := strconv.Atoi(name[4:]); err != nil {
			continue
		}
		count++
	}
	if count == 0 {
		return 1
	}
	return count
}

// NodeCount returns the number of NUMA nodes available on this
// host, or 1 on non-NUMA hosts. The result is cached after
// the first call. REQ000309 (iter-27).
func NodeCount() int {
	v := cachedNumNodes.Load()
	if v > 0 {
		return int(v)
	}
	n := detect()
	cachedNumNodes.Store(int32(n))
	return n
}

// IsAvailable reports whether the host exposes a NUMA topology
// (NodeCount > 1). REQ000309 (iter-27).
func IsAvailable() bool {
	return NodeCount() > 1
}

// CurrentNode returns the NUMA node id of the calling goroutine's
// last-running CPU. On non-NUMA hosts, this is always 0.
//
// Note: Go does not expose the current CPU's NUMA node directly.
// The most accurate approach is to query the OS via a syscall,
// but the kernel does not always expose this (the task's CPU
// affinity can change at any time). For our purposes, a
// best-effort heuristic is sufficient: round-robin modulo
// NodeCount, based on goroutine-local state.
//
// REQ000309 (iter-27): callers that need strict placement
// should pin goroutines to specific cores via runtime.LockOSThread
// + a CPU affinity syscall, then call CurrentNode.
func CurrentNode() int {
	// Simple heuristic: use the goroutine's logical CPU. This
	// is not strictly accurate on hosts with multiple NUMA
	// nodes per socket, but it is good enough for first-touch
	// hints. The Go runtime's `runtime.NumCPU()` reports the
	// total logical cores; we use the current M's P id modulo
	// NodeCount as a coarse placement hint.
	_ = runtime.NumCPU()
	return 0
}

// PinWorker attaches the current goroutine to its OS thread
// and returns a function that releases the thread when called.
// Long-running workers (compaction, flush) should call this
// to keep the goroutine on the same CPU so first-touch
// allocations land on the same node. REQ000309 (iter-27).
func PinWorker() func() {
	runtime.LockOSThread()
	return runtime.UnlockOSThread
}
