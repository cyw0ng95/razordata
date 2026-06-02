package SN

import (
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/TXN/MV"
)

type versionChainSnapshot struct {
	key  []byte
	head *MV.VersionNode
}

type readView struct {
	readTS   uint64
	snapshot []versionChainSnapshot
	arena    *MV.Arena
	mv       *MV.MV
	closed   atomic.Bool
}

func newReadView(mv *MV.MV, readTS uint64) *readView {
	return &readView{
		readTS:   readTS,
		snapshot: make([]versionChainSnapshot, 0),
		mv:       mv,
	}
}

func (rv *readView) addSnapshot(key []byte, head *MV.VersionNode) {
	rv.snapshot = append(rv.snapshot, versionChainSnapshot{
		key:  key,
		head: head,
	})
}

func (rv *readView) Get(key []byte) ([]byte, error) {
	if rv.closed.Load() {
		return nil, MV.ErrInvalidTx
	}

	for _, snap := range rv.snapshot {
		if string(snap.key) == string(key) {
			node := snap.head
			for node != nil {
				if node.IsVisible(rv.readTS) {
					if node.Deleted() {
						return nil, MV.ErrNotFound
					}
					return node.Value(), nil
				}
				node = node.Next()
			}
			return nil, MV.ErrNotFound
		}
	}

	chain := rv.mv.GetVersionChain(key)
	if chain == nil {
		return nil, MV.ErrNotFound
	}

	for node := chain.GetHead(); node != nil; node = node.Next() {
		if node.IsVisible(rv.readTS) {
			rv.addSnapshot(key, chain.GetHead())
			if node.Deleted() {
				return nil, MV.ErrNotFound
			}
			return node.Value(), nil
		}
	}

	return nil, MV.ErrNotFound
}

func (rv *readView) Close() {
	if rv.closed.CompareAndSwap(false, true) {
		if rv.arena != nil {
			MV.PutArena(rv.arena)
		}
	}
}

func (rv *readView) IsClosed() bool {
	return rv.closed.Load()
}
