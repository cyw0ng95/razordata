package ls

// CompactionStyle selects the compaction strategy. REQ000320.
// Leveled is the default: each level is a sorted run, L(n+1) is
// ~10x the size of L(n), and compaction picks one level at a time
// when its size budget is exceeded. Best for point lookups and
// range scans.
// Tiered groups multiple sorted runs at the same level and only
// merges when the run count exceeds a threshold. Best for
// write-heavy workloads (time-series, ingest).
// Hybrid combines both: tiered for L0 (write amplification),
// leveled for L1+.
type CompactionStyle int

const (
	// CompactionStyleLeveled is the default. Each L(n) has a
	// size budget; compaction runs when L(n) exceeds it.
	CompactionStyleLeveled CompactionStyle = iota
	// CompactionStyleTiered groups sorted runs; merges when
	// run count > tierThreshold (default 4).
	CompactionStyleTiered
	// CompactionStyleHybrid uses tiered for L0 and leveled for
	// L1+. Best general-purpose setting.
	CompactionStyleHybrid
)

// String returns the lowercase name of the style.
func (s CompactionStyle) String() string {
	switch s {
	case CompactionStyleLeveled:
		return "leveled"
	case CompactionStyleTiered:
		return "tiered"
	case CompactionStyleHybrid:
		return "hybrid"
	}
	return "leveled"
}

// IsTiered returns true if the style allows multiple sorted runs
// at a given level. Hybrid returns true for L0 and false for L1+.
func (s CompactionStyle) IsTiered(level int) bool {
	switch s {
	case CompactionStyleLeveled:
		return false
	case CompactionStyleTiered:
		return true
	case CompactionStyleHybrid:
		return level == 0
	}
	return false
}

// tierRunThreshold is the maximum number of sorted runs before a
// tiered compaction fires. REQ000320.
const tierRunThreshold = 4

// StoragePolicy selects the device-tier strategy for placing SST files.
// REQ000300.
type StoragePolicy int

const (
	// StoragePolicyUniform places all levels on the same device (default).
	StoragePolicyUniform StoragePolicy = iota
	// StoragePolicyTiered places levels on devices according to
	// PlacementPolicy: lower levels (hot) on fast devices, higher
	// levels (cold) on slower/cheaper devices.
	StoragePolicyTiered
)

// String returns the lowercase name of the storage policy.
func (p StoragePolicy) String() string {
	switch p {
	case StoragePolicyUniform:
		return "uniform"
	case StoragePolicyTiered:
		return "tiered"
	}
	return "uniform"
}

// PlacementPolicy maps a compaction output level to a device base path.
// An empty map or a missing level uses the engine directory. If tiered
// placement is active, output SST files for level N are written to the
// mapped path (with an sst/ subdirectory). A symlink is created in the
// engine dir so the merge iterator finds the file transparently.
// REQ000300.
type PlacementPolicy map[int]string

// DeviceDir returns the device path for level, or the fallback dir if
// no policy entry exists.
func (pp PlacementPolicy) DeviceDir(level int, fallback string) string {
	if pp == nil {
		return fallback
	}
	if d, ok := pp[level]; ok && d != "" {
		return d
	}
	return fallback
}

// shouldCompact reports whether a compaction is needed at the given
// level, given the style and the number of files at that level plus
// the cumulative size in bytes.
func (s CompactionStyle) shouldCompact(level int, fileCount int, sizeBytes int64) bool {
	switch s {
	case CompactionStyleLeveled:
		// Leveled: compact when size > budget for this level.
		return sizeBytes > defaultBudget.budgetFor(level)
	case CompactionStyleTiered:
		// Tiered: compact when run count > threshold.
		return fileCount > tierRunThreshold
	case CompactionStyleHybrid:
		if level == 0 {
			return fileCount > tierRunThreshold
		}
		return sizeBytes > defaultBudget.budgetFor(level)
	}
	return false
}
