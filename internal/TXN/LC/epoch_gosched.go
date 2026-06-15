package LC

import "runtime"

// This file implements runtime.Gosched for WaitForDrain.
// Split into its own file so the hot Reclaim path doesn't
// pull in the runtime import.

func init() {
	runtime_GoschedFn = runtime.Gosched
}
