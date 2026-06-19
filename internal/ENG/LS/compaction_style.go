package ls

// CompactionStyle selects the compaction strategy (REQ000320).
type CompactionStyle int

const (
	CompactionStyleLeveled CompactionStyle = iota
	CompactionStyleTiered
	CompactionStyleHybrid
)

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

// IsTiered returns true if the style allows multiple sorted runs at a level.
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

const tierRunThreshold = 4

// StoragePolicy selects the device-tier strategy for SST placement (REQ000300).
type StoragePolicy int

const (
	StoragePolicyUniform StoragePolicy = iota
	StoragePolicyTiered
)

func (p StoragePolicy) String() string {
	switch p {
	case StoragePolicyUniform:
		return "uniform"
	case StoragePolicyTiered:
		return "tiered"
	}
	return "uniform"
}

// PlacementPolicy maps a compaction output level to a device path (REQ000300).
type PlacementPolicy map[int]string

// DeviceDir returns the device path for level, or fallback.
func (pp PlacementPolicy) DeviceDir(level int, fallback string) string {
	if pp == nil {
		return fallback
	}
	if d, ok := pp[level]; ok && d != "" {
		return d
	}
	return fallback
}

func (s CompactionStyle) shouldCompact(level int, fileCount int, sizeBytes int64) bool {
	switch s {
	case CompactionStyleLeveled:
		return sizeBytes > defaultBudget.budgetFor(level)
	case CompactionStyleTiered:
		return fileCount > tierRunThreshold
	case CompactionStyleHybrid:
		if level == 0 {
			return fileCount > tierRunThreshold
		}
		return sizeBytes > defaultBudget.budgetFor(level)
	}
	return false
}
