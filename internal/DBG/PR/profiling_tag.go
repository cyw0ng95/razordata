//go:build dbg_profiling

package pr

import "runtime"

func init() {
	runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(1)
}
