package MV

import (
	"math"
	"sync/atomic"
	"unsafe"
)

// VersionNode is a single version entry in a version chain.
type VersionNode struct {
	txnID   uint64
	beginTS uint64
	endTS   atomic.Uint64
	key     []byte
	value   []byte
	deleted bool
	next    atomic.Pointer[VersionNode]
}

// noescape hides a pointer from escape analysis (REQ000306).
//
//go:nosplit
func noescape(p unsafe.Pointer) unsafe.Pointer {
	x := uintptr(p)
	return unsafe.Pointer(x ^ 0)
}

// NewVersionNode allocates a VersionNode out of the supplied arena.
func NewVersionNode(arena *Arena, txnID, beginTS uint64, key, value []byte, deleted bool) *VersionNode {
	size := int(unsafe.Sizeof(VersionNode{}))
	mem := arena.Alloc(size)
	if mem == nil {
		var n VersionNode
		node := &n
		node.txnID = txnID
		node.beginTS = beginTS
		node.endTS.Store(math.MaxUint64)
		node.key = key
		node.value = value
		node.deleted = deleted
		return node
	}
	node := (*VersionNode)(unsafe.Pointer(&mem[0]))
	node.txnID = txnID
	node.beginTS = beginTS
	node.endTS.Store(math.MaxUint64)
	node.key = key
	node.value = value
	node.deleted = deleted
	return node
}

func (n *VersionNode) IsUncommitted() bool {
	return n.endTS.Load() == math.MaxUint64
}

// NewVersionNodeStack allocates a VersionNode on the caller's stack (REQ000306).
func NewVersionNodeStack(txnID, beginTS uint64, key, value []byte, deleted bool) *VersionNode {
	var n VersionNode
	n.txnID = txnID
	n.beginTS = beginTS
	n.endTS.Store(math.MaxUint64)
	n.key = key
	n.value = value
	n.deleted = deleted
	return (*VersionNode)(noescape(unsafe.Pointer(&n)))
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

func (vc *VersionChain) Head() *VersionNode {
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
	return n.endTS.CompareAndSwap(math.MaxUint64, commitTS)
}

func (vc *VersionChain) Commit(node *VersionNode, commitTS uint64) bool {
	return node.Commit(commitTS)
}

func (vc *VersionChain) FindVisible(readTS uint64) *VersionNode {
	for node := vc.Head(); node != nil; node = node.next.Load() {
		if node.IsUncommitted() {
			continue
		}
		if node.IsVisible(readTS) {
			return node
		}
	}
	return nil
}
