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

func (h *hazardPointerSet) Publish(ptr unsafe.Pointer) {
	for i := 0; i < MaxHazardPtrs; i++ {
		h.ptrs[i].Store(ptr)
	}
}

func (h *hazardPointerSet) Clear() {
	for i := 0; i < MaxHazardPtrs; i++ {
		h.ptrs[i].Store(unsafe.Pointer(nil))
	}
}

func (h *hazardPointerSet) Scan() []unsafe.Pointer {
	result := make([]unsafe.Pointer, 0, MaxHazardPtrs)
	for i := 0; i < MaxHazardPtrs; i++ {
		p := h.ptrs[i].Load().(unsafe.Pointer)
		if p != nil {
			result = append(result, p)
		}
	}
	return result
}
