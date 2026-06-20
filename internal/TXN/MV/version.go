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

// IsUncommitted returns true if the node has not yet been committed
// (endTS is still MaxUint64).
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

// IsVisible returns true if the node is visible at the given read timestamp.
func (n *VersionNode) IsVisible(readTS uint64) bool {
	return n.beginTS < readTS && n.endTS.Load() >= readTS
}

// TxnID returns the transaction ID that created this version.
func (n *VersionNode) TxnID() uint64 {
	return n.txnID
}

// BeginTS returns the begin timestamp of this version.
func (n *VersionNode) BeginTS() uint64 {
	return n.beginTS
}

// EndTS returns the end timestamp of this version.
func (n *VersionNode) EndTS() uint64 {
	return n.endTS.Load()
}

// Key returns the row key for this version.
func (n *VersionNode) Key() []byte {
	return n.key
}

// Value returns the value stored in this version.
func (n *VersionNode) Value() []byte {
	return n.value
}

// Deleted returns true if this version represents a deletion tombstone.
func (n *VersionNode) Deleted() bool {
	return n.deleted
}

// Next returns the next older version in the chain, or nil.
func (n *VersionNode) Next() *VersionNode {
	return n.next.Load()
}

// VersionChain is a lock-free singly-linked list of version nodes for a
// single row key, ordered from newest to oldest.
type VersionChain struct {
	head atomic.Pointer[VersionNode]
}

// Head returns the newest node in the chain, or nil if empty.
func (vc *VersionChain) Head() *VersionNode {
	return vc.head.Load()
}

// Insert prepends a node to the head of the chain using CAS. Returns true
// on success.
func (vc *VersionChain) Insert(node *VersionNode) bool {
	for {
		oldHead := vc.head.Load()
		node.next.Store(oldHead)
		if vc.head.CompareAndSwap(oldHead, node) {
			return true
		}
	}
}

// Commit marks the node as committed at commitTS. Returns false if the node
// was already committed.
func (n *VersionNode) Commit(commitTS uint64) bool {
	return n.endTS.CompareAndSwap(math.MaxUint64, commitTS)
}

// Commit marks the given node as committed at commitTS. Returns false if the
// node was already committed.
func (vc *VersionChain) Commit(node *VersionNode, commitTS uint64) bool {
	return node.Commit(commitTS)
}

// FindVisible returns the first committed node in the chain that is visible
// at readTS, or nil if none matches.
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
