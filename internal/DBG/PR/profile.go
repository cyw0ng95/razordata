//go:build debug

package pr

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"runtime/trace"
	"time"
)

var profileDir string

// SetProfileDir sets the output directory for profile dumps.
func SetProfileDir(dir string) { profileDir = dir }

// DumpProfile writes a pprof profile to disk and returns the path.
func DumpProfile(kind string, dur time.Duration) (string, error) {
	if profileDir == "" {
		profileDir = "."
	}
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return "", fmt.Errorf("create profile dir: %w", err)
	}

	ts := time.Now().UnixNano()
	filename := fmt.Sprintf("%s-%d.pprof", kind, ts)
	path := filepath.Join(profileDir, filename)

	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create profile file: %w", err)
	}
	defer f.Close()

	switch kind {
	case "cpu":
		if err := pprof.StartCPUProfile(f); err != nil {
			return "", fmt.Errorf("start cpu profile: %w", err)
		}
		time.Sleep(dur)
		pprof.StopCPUProfile()
	case "trace":
		if err := trace.Start(f); err != nil {
			return "", fmt.Errorf("start trace: %w", err)
		}
		time.Sleep(dur)
		trace.Stop()
	default:
		prof := pprof.Lookup(kind)
		if prof == nil {
			return "", fmt.Errorf("unknown profile kind: %s", kind)
		}
		if err := prof.WriteTo(f, 0); err != nil {
			return "", fmt.Errorf("write %s profile: %w", kind, err)
		}
	}

	return path, nil
}
