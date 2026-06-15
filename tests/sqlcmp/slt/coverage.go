package slt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// CoverageSnapshot is the per-file pass/fail/skip record
// emitted after a corpus run. The file is the unit of
// regression detection: any per-file drop relative to the
// baseline flips the run to red.
type CoverageSnapshot struct {
	Version   string                   `json:"version"`
	Threshold float64                  `json:"threshold"`
	Files     map[string]CoverageEntry `json:"files"`
	Totals    Stats                    `json:"totals"`
}

// CoverageEntry is the per-file stats. PassRate is computed
// lazily; we store raw counters so the snapshot is
// reproducible across runs even if the diff policy changes.
type CoverageEntry struct {
	Total       int `json:"total"`
	Passed      int `json:"passed"`
	Failed      int `json:"failed"`
	Skipped     int `json:"skipped"`
	ParseErrors int `json:"parse_errors"`
}

// writeCoverageSnapshot emits a JSON file at path summarising
// the per-file outcomes. The directory is created if missing.
func writeCoverageSnapshot(path string, files []fileStat, total Stats, threshold float64) error {
	snap := CoverageSnapshot{
		Version:   "1",
		Threshold: threshold,
		Files:     make(map[string]CoverageEntry, len(files)),
		Totals:    total,
	}
	for _, f := range files {
		snap.Files[f.path] = CoverageEntry{
			Total:       f.stats.Total,
			Passed:      f.stats.Passed,
			Failed:      f.stats.Failed,
			Skipped:     f.stats.Skipped,
			ParseErrors: f.stats.ParseErrors,
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Sort the keys for deterministic output.
	keys := make([]string, 0, len(snap.Files))
	for k := range snap.Files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := CoverageSnapshot{
		Version:   snap.Version,
		Threshold: snap.Threshold,
		Totals:    snap.Totals,
		Files:     make(map[string]CoverageEntry, len(snap.Files)),
	}
	for _, k := range keys {
		ordered.Files[k] = snap.Files[k]
	}
	data, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// compareToBaseline returns a list of file paths whose pass
// count dropped below the recorded baseline. Files missing
// from the baseline are treated as "new" and are not flagged.
// An empty diff slice means "no regression".
func compareToBaseline(baseline, current *CoverageSnapshot) []string {
	var drops []string
	if baseline == nil {
		return drops
	}
	for path, cur := range current.Files {
		base, ok := baseline.Files[path]
		if !ok {
			continue
		}
		if cur.Passed < base.Passed {
			drops = append(drops, path)
		}
	}
	return drops
}

// loadBaseline reads a baseline snapshot from disk. A
// missing file is not an error; the caller treats it as
// "no baseline yet".
func loadBaseline(path string) (*CoverageSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var snap CoverageSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}
