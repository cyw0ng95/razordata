package wr

import "sync/atomic"

// atomicBool is a tiny atomic boolean used for the closed flag.
// Kept in its own file so the implementation detail doesn't clutter
// the type definitions in wr.go.
type atomicBool struct {
	v atomic.Uint32
}

func (a *atomicBool) isSet() bool { return a.v.Load() != 0 }
func (a *atomicBool) set() bool   { return a.v.CompareAndSwap(0, 1) }
func (a *atomicBool) clear()      { a.v.Store(0) }
