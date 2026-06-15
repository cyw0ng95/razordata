package LC

import (
	"sync/atomic"
	"unsafe"
)

const MaxHazardPtrs = 2

type hazardPointerSet struct {
	ptrs [MaxHazardPtrs]atomic.Value
}

func newHazardPointerSet() *hazardPointerSet {
	return &hazardPointerSet{}
}

// PublishCurrent publishes ptr to slot 0 (the 'current read'
// pointer). Per TXN.md:96-121, the double-slot design lets a
// reader hold the current node in slot 0 while prefetching
// the next node into slot 1 — see PublishNext.
//
// Before iter-15 (REQ000158) this function was called
// 'Publish' and wrote to ALL slots, which defeats the
// double-slot design (slot 1 would hold the wrong pointer
// if a different one is meant to be prefetched).
func (h *hazardPointerSet) PublishCurrent(ptr unsafe.Pointer) {
	h.ptrs[0].Store(ptr)
}

// PublishNext publishes ptr to slot 1 (the 'prefetch' pointer).
// Callers PublishCurrent(A) first, then PublishNext(B), so a
// reclaimer seeing slot 0 = A and slot 1 = B knows not to
// reclaim either.
func (h *hazardPointerSet) PublishNext(ptr unsafe.Pointer) {
	h.ptrs[1].Store(ptr)
}

func (h *hazardPointerSet) Clear() {
	h.ptrs[0].Store(unsafe.Pointer(nil))
	h.ptrs[1].Store(unsafe.Pointer(nil))
}

func (h *hazardPointerSet) Scan() []unsafe.Pointer {
	result := make([]unsafe.Pointer, 0, MaxHazardPtrs)
	for i := 0; i < MaxHazardPtrs; i++ {
		v := h.ptrs[i].Load()
		if v == nil {
			continue
		}
		p, ok := v.(unsafe.Pointer)
		if !ok || p == nil {
			continue
		}
		result = append(result, p)
	}
	return result
}
