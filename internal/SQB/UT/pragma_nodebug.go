//go:build !debug

package UT

import "github.com/cyw0ng95/razordata/internal/SQB/DT"

// HandleDebugPragma is a no-op stub for non-debug builds.
func HandleDebugPragma(_ string, _ []string) (string, error) {
	return "", DT.ErrRequiresDebugBuild
}