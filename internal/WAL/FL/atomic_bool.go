package fl

import "sync/atomic"

// atomicBool is a tiny atomic boolean used for the closed flag.
// Identical to the one in WR; duplicated rather than shared to keep
// each cluster self-contained.
type atomicBool struct {
	v atomic.Uint32
}

func (a *atomicBool) isSet() bool { return a.v.Load() != 0 }
func (a *atomicBool) set() bool   { return a.v.CompareAndSwap(0, 1) }
func (a *atomicBool) clear()      { a.v.Store(0) }
