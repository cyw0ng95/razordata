//go:build !linux

package bf

// madviseDontNeed is a no-op on non-Linux platforms. Tests use the
// package-level madviseFn var on Linux; this stub keeps the build
// matrix clean.
func madviseDontNeed(buf []byte) {}
