package MV

import (
	"sync/atomic"
	"unsafe"
)

const maxUint64 = ^uint64(0)

type VersionNode struct {
	txnID   uint64
	beginTS uint64
	endTS   atomic.Uint64
	key     []byte
	value   []byte
	deleted bool
	next    atomic.Pointer[VersionNode]
}

// NewVersionNode allocates a VersionNode out of the supplied arena and
// initializes it. The arena is expected to be the per-transaction arena
// owned by the calling transaction; the node lives as long as the arena
// backs its memory and any live version chain references it.
//
// The arena's bump pointer is CAS-safe on its own, but the arena itself
// is NOT safe for concurrent use by multiple goroutines — the caller
// (the *tx* type in VL/protocol.go) holds a per-txn mutex that
// serializes Insert/Delete and therefore serializes arena access.
func NewVersionNode(arena *Arena, txnID, beginTS uint64, key, value []byte, deleted bool) *VersionNode {
	size := int(unsafe.Sizeof(VersionNode{}))
	mem := arena.Alloc(size)
	if mem == nil {
		// Arena exhausted: fall back to a heap allocation. The next Insert
		// on the same transaction will then resume from the arena.
		// Returning a heap-allocated node preserves correctness; the
		// memory is reclaimed by Go's GC once the node is unreferenced.
		var n VersionNode
		node := &n
		node.txnID = txnID
		node.beginTS = beginTS
		node.endTS.Store(maxUint64)
		node.key = key
		node.value = value
		node.deleted = deleted
		return node
	}
	node := (*VersionNode)(unsafe.Pointer(&mem[0]))
	node.txnID = txnID
	node.beginTS = beginTS
	node.endTS.Store(maxUint64)
	node.key = key
	node.value = value
	node.deleted = deleted
	return node
}

func (n *VersionNode) IsUncommitted() bool {
	return n.endTS.Load() == maxUint64
}

func (n *VersionNode) IsVisible(readTS uint64) bool {
	return n.beginTS < readTS && n.endTS.Load() >= readTS
}

func (n *VersionNode) TxnID() uint64 {
	return n.txnID
}

func (n *VersionNode) BeginTS() uint64 {
	return n.beginTS
}

func (n *VersionNode) EndTS() uint64 {
	return n.endTS.Load()
}

func (n *VersionNode) Key() []byte {
	return n.key
}

func (n *VersionNode) Value() []byte {
	return n.value
}

func (n *VersionNode) Deleted() bool {
	return n.deleted
}

func (n *VersionNode) Next() *VersionNode {
	return n.next.Load()
}

type VersionChain struct {
	head atomic.Pointer[VersionNode]
}

func (vc *VersionChain) GetHead() *VersionNode {
	return vc.head.Load()
}

func (vc *VersionChain) Insert(node *VersionNode) bool {
	for {
		oldHead := vc.head.Load()
		node.next.Store(oldHead)
		if vc.head.CompareAndSwap(oldHead, node) {
			return true
		}
	}
}

func (n *VersionNode) Commit(commitTS uint64) bool {
	return n.endTS.CompareAndSwap(maxUint64, commitTS)
}

func (vc *VersionChain) Commit(node *VersionNode, commitTS uint64) bool {
	return node.Commit(commitTS)
}

func (vc *VersionChain) FindVisible(readTS uint64) *VersionNode {
	for node := vc.GetHead(); node != nil; node = node.next.Load() {
		if node.IsVisible(readTS) {
			return node
		}
	}
	return nil
}
