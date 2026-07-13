// Package nm implements NUMA topology detection (REQ000309).
package nm

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
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

// currentCPU reads the CPU id from /proc/self/stat (field 39, zero-indexed 38).
// Returns -1 on any error (e.g. container without /proc).
func currentCPU() int {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return -1
	}
	// Fields are space-separated. Field 39 (1-indexed) is cpu.
	// Count spaces from the end of the comm field (which is in parens
	// and may contain spaces). Find the closing ')' first.
	idx := strings.LastIndexByte(string(data), ')')
	if idx < 0 {
		return -1
	}
	fields := strings.Fields(string(data[idx+2:]))
	if len(fields) < 38 {
		return -1
	}
	cpu, err := strconv.Atoi(fields[37])
	if err != nil {
		return -1
	}
	return cpu
}

// cpuToNode maps a CPU id to its NUMA node via
// /sys/devices/system/cpu/cpuN/node. Returns 0 on error.
func cpuToNode(cpu int) int {
	path := "/sys/devices/system/cpu/cpu" + strconv.Itoa(cpu) + "/node"
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

// singleNodeFastPath is set once via sync.Once on the first
// CurrentNode() call. When true (the typical case — single-socket
// servers, containers, dev laptops), CurrentNode bypasses
// /proc/self/stat + sysfs reads and returns 0. The single-node
// detection reuses cachedNumNodes so the actual probe happens
// at most once per process.
//
// REQ001421 (alloc reduction): the original 4.85x INSERT gap vs
// modernc.org/sqlite is dominated by /proc/self/stat reads on
// every AcquireArena/PutArena call (~308 MB of 961 MB total
// per-INSERT-iteration allocations). Short-circuiting here
// collapses that to zero on single-node hosts.
var singleNodeFastPath atomic.Bool
var singleNodeFastPathOnce sync.Once

// CurrentNode returns the NUMA node id of the calling goroutine (REQ000309).
// On single-node hosts (the dominant case) this is a single atomic
// load returning 0. Multi-node hosts fall back to currentCPU +
// cpuToNode (one /proc + one sysfs read per call).
func CurrentNode() int {
	singleNodeFastPathOnce.Do(func() {
		singleNodeFastPath.Store(NodeCount() <= 1)
	})
	if singleNodeFastPath.Load() {
		return 0
	}
	cpu := currentCPU()
	if cpu < 0 {
		return 0
	}
	return cpuToNode(cpu)
}

// PinWorker pins the current goroutine to its OS thread (REQ000309).
func PinWorker() func() {
	runtime.LockOSThread()
	return runtime.UnlockOSThread
}

// Topology holds the detected NUMA topology for a host.
// It maps each NUMA node to the set of CPU ids on that node.
type Topology struct {
	NodeCount int
	NodeCPUs  map[int][]int // node → cpu ids
	cpuToNode map[int]int   // cpu → node (reverse index)
}

var (
	cachedTopology     *Topology
	cachedTopologyOnce sync.Once
)

// GetTopology returns the detected NUMA topology, creating it on first call.
// On non-NUMA hosts, returns a single-node topology with all CPUs.
func GetTopology() *Topology {
	cachedTopologyOnce.Do(func() {
		cachedTopology = detectTopology()
	})
	return cachedTopology
}

func detectTopology() *Topology {
	nodeCount := NodeCount()
	t := &Topology{
		NodeCount: nodeCount,
		NodeCPUs:  make(map[int][]int, nodeCount),
		cpuToNode: make(map[int]int),
	}

	// On non-NUMA hosts, all CPUs map to node 0.
	if nodeCount <= 1 {
		ncpu := runtime.NumCPU()
		cpus := make([]int, ncpu)
		for i := range cpus {
			cpus[i] = i
		}
		t.NodeCPUs[0] = cpus
		for _, c := range cpus {
			t.cpuToNode[c] = 0
		}
		return t
	}

	// Multi-NUMA: read /sys/devices/system/node/nodeN/cpulist.
	for node := 0; node < nodeCount; node++ {
		path := "/sys/devices/system/node/node" + strconv.Itoa(node) + "/cpulist"
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		cpus := parseCPURange(strings.TrimSpace(string(data)))
		t.NodeCPUs[node] = cpus
		for _, c := range cpus {
			t.cpuToNode[c] = node
		}
	}

	// Fallback: if sysfs didn't populate any nodes, assign all CPUs to node 0.
	if len(t.NodeCPUs) == 0 {
		ncpu := runtime.NumCPU()
		cpus := make([]int, ncpu)
		for i := range cpus {
			cpus[i] = i
		}
		t.NodeCPUs[0] = cpus
		for _, c := range cpus {
			t.cpuToNode[c] = 0
		}
	}

	return t
}

// parseCPURange parses a Linux CPU list like "0-3,8-11" into []int.
func parseCPURange(s string) []int {
	var cpus []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.IndexByte(part, '-'); idx >= 0 {
			lo, err1 := strconv.Atoi(part[:idx])
			hi, err2 := strconv.Atoi(part[idx+1:])
			if err1 != nil || err2 != nil {
				continue
			}
			for i := lo; i <= hi; i++ {
				cpus = append(cpus, i)
			}
		} else {
			c, err := strconv.Atoi(part)
			if err != nil {
				continue
			}
			cpus = append(cpus, c)
		}
	}
	return cpus
}

// NodeForCPU returns the NUMA node for a given CPU id.
// Returns 0 if the CPU is not found in the topology.
func (t *Topology) NodeForCPU(cpu int) int {
	if n, ok := t.cpuToNode[cpu]; ok {
		return n
	}
	return 0
}

// CPUsForNode returns the CPU ids for a given NUMA node.
// Returns nil if the node is not in the topology.
func (t *Topology) CPUsForNode(node int) []int {
	return t.NodeCPUs[node]
}
