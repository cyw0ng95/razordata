package MV

import (
	"sync/atomic"
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

func newVersionNode(txnID, beginTS uint64, key, value []byte, deleted bool) *VersionNode {
	node := allocVersionNode()
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

func (vc *VersionChain) Commit(node *VersionNode, commitTS uint64) bool {
	return node.endTS.CompareAndSwap(maxUint64, commitTS)
}

func (vc *VersionChain) FindVisible(readTS uint64) *VersionNode {
	for node := vc.GetHead(); node != nil; node = node.next.Load() {
		if node.IsVisible(readTS) {
			return node
		}
	}
	return nil
}
