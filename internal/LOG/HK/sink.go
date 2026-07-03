//go:build debug

package hk

func init() {
	// DefaultSink and DefaultMetricSink are swapped by DBG init() at startup.
	// This file exists so that the debug build compiles the real DBG packages.
}
