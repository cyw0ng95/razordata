//go:build debug

package hk

// This file is intentionally empty when built with the debug tag.
// Global swapping (DefaultSink, DefaultMetricSink) is done by DBG/dbg.go
// at startup to avoid import cycles (LOG/HK → DBG/CT → LOG/HK).
