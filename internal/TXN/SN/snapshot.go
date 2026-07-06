package SN

import (
	"sync"
	"sync/atomic"

	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
	"github.com/cyw0ng95/razordata/internal/TXN/MV"
)

type VersionChainSnapshot struct {
	Key  []byte
	Head *MV.VersionNode
}

func NewReadView(mv *MV.MV, readTS, maxCommittedTS uint64) *ReadView {
	EC.BUG_ON(mv == nil, "sn.NewReadView: nil MV")
	EC.WARN_ON(readTS == 0, "sn.NewReadView: zero readTS — snapshot may be invalid")
	EC.WARN_ON(readTS+1 < maxCommittedTS, "sn.NewReadView: readTS %d is %d behind latest commit %d — snapshot lag", readTS, maxCommittedTS-readTS, maxCommittedTS)
	return &ReadView{
		readTS:   readTS,
		snapshot: make(map[string]*VersionChainSnapshot),
		mv:       mv,
	}
}

type ReadView struct {
	readTS   uint64
	snapshot map[string]*VersionChainSnapshot
	arena    *MV.Arena
	mv       *MV.MV
	closed   atomic.Bool
	mu       sync.Mutex
}

func (rv *ReadView) addSnapshot(key []byte, head *MV.VersionNode) {
	rv.mu.Lock()
	defer rv.mu.Unlock()
	rv.snapshot[string(key)] = &VersionChainSnapshot{
		Key:  key,
		Head: head,
	}
}

func (rv *ReadView) Get(key []byte) ([]byte, error) {
	if rv.closed.Load() {
		return nil, MV.ErrInvalidTx
	}

	rv.mu.Lock()
	snap := rv.snapshot[string(key)]
	rv.mu.Unlock()

	if snap != nil {
		for node := snap.Head; node != nil; node = node.Next() {
			if node.IsVisible(rv.readTS) {
				if node.Deleted() {
					return nil, MV.ErrNotFound
				}
				return node.Value(), nil
			}
		}
		return nil, MV.ErrNotFound
	}

	chain := rv.mv.VersionChain(key)
	if chain == nil {
		return nil, MV.ErrNotFound
	}

	for node := chain.Head(); node != nil; node = node.Next() {
		if node.IsVisible(rv.readTS) {
			rv.addSnapshot(key, chain.Head())
			if node.Deleted() {
				return nil, MV.ErrNotFound
			}
			return node.Value(), nil
		}
	}

	return nil, MV.ErrNotFound
}

func (rv *ReadView) Close() {
	if rv.closed.CompareAndSwap(false, true) {
		if rv.arena != nil {
			MV.PutArena(rv.arena)
		}
	}
}

func (rv *ReadView) IsClosed() bool {
	return rv.closed.Load()
}
