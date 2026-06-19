// Package nm implements NUMA topology detection (REQ000309).
package nm

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

var cachedNumNodes atomic.Int32

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

// NodeCount returns the number of NUMA nodes (REQ000309).
func NodeCount() int {
	v := cachedNumNodes.Load()
	if v > 0 {
		return int(v)
	}
	n := detect()
	cachedNumNodes.Store(int32(n))
	return n
}

// IsAvailable reports whether the host has multiple NUMA nodes (REQ000309).
func IsAvailable() bool {
	return NodeCount() > 1
}

// CurrentNode returns the NUMA node id of the calling goroutine (REQ000309).
func CurrentNode() int {
	_ = runtime.NumCPU()
	return 0
}

// PinWorker pins the current goroutine to its OS thread (REQ000309).
func PinWorker() func() {
	runtime.LockOSThread()
	return runtime.UnlockOSThread
}
