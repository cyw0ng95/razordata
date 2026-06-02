package MV

import (
	"sync/atomic"
)

const maxUint64 = ^uint64(0)

type versionNode struct {
	txnID   uint64
	beginTS uint64
	endTS   atomic.Uint64
	key     []byte
	value   []byte
	deleted bool
	next    atomic.Pointer[versionNode]
}

func newVersionNode(txnID, beginTS uint64, key, value []byte, deleted bool) *versionNode {
	node := allocVersionNode()
	node.txnID = txnID
	node.beginTS = beginTS
	node.endTS.Store(maxUint64)
	node.key = key
	node.value = value
	node.deleted = deleted
	return node
}

func (n *versionNode) IsUncommitted() bool {
	return n.endTS.Load() == maxUint64
}

func (n *versionNode) IsVisible(readTS uint64) bool {
	return n.beginTS < readTS && n.endTS.Load() >= readTS
}

type versionChain struct {
	head atomic.Pointer[versionNode]
}

func (vc *versionChain) GetHead() *versionNode {
	return vc.head.Load()
}

func (vc *versionChain) Insert(node *versionNode) bool {
	for {
		oldHead := vc.head.Load()
		node.next.Store(oldHead)
		if vc.head.CompareAndSwap(oldHead, node) {
			return true
		}
	}
}

func (vc *versionChain) Commit(node *versionNode, commitTS uint64) bool {
	return node.endTS.CompareAndSwap(maxUint64, commitTS)
}

func (vc *versionChain) FindVisible(readTS uint64) *versionNode {
	for node := vc.GetHead(); node != nil; node = node.next.Load() {
		if node.IsVisible(readTS) {
			return node
		}
	}
	return nil
}
