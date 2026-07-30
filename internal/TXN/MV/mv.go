package MV

import (
	"sync"
	"unsafe"
)

// bytesToString converts []byte to string without allocation.
// REQ002050 SAFETY: the input slice must not be modified after this
// call, as the returned string shares the same backing array. The
// arena life-cycle ensures stability — arena buffers are only recycled
// after all version chains referencing them are released.
func bytesToString(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

// MV is a multi-version concurrency control map that stores version chains
// keyed by row key. It uses sync.Map for lock-free concurrent reads.
type MV struct {
	chains sync.Map
}

// NewMV creates a new empty MV instance.
func NewMV() *MV {
	return &MV{}
}

// VersionChain returns the version chain for the given key, or nil if no
// chain exists.
func (m *MV) VersionChain(key []byte) *VersionChain {
	chain, ok := m.chains.Load(bytesToString(key))
	if !ok {
		return nil
	}
	return chain.(*VersionChain)
}

// EnsureVersionChain returns the existing version chain for key or creates a
// new empty chain atomically.
func (m *MV) EnsureVersionChain(key []byte) *VersionChain {
	chainI, _ := m.chains.LoadOrStore(bytesToString(key), &VersionChain{})
	return chainI.(*VersionChain)
}

// Insert appends a version node to the chain for key. The chain is created
// if it does not exist. Returns true on success.
func (m *MV) Insert(key []byte, node *VersionNode) bool {
	chain := m.EnsureVersionChain(key)
	return chain.Insert(node)
}

// FindVisible returns the first committed version node for key that is
// visible at readTS, or nil if no such version exists.
func (m *MV) FindVisible(key []byte, readTS uint64) *VersionNode {
	chain := m.VersionChain(key)
	if chain == nil {
		return nil
	}
	return chain.FindVisible(readTS)
}

// Commit marks the given node as committed at commitTS. Returns false if the
// node was already committed.
func (m *MV) Commit(node *VersionNode, commitTS uint64) bool {
	return node.Commit(commitTS)
}
