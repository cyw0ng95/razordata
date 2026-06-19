package MV

import (
	"sync"
	"unsafe"
)

// bytesToString converts []byte to string without allocation.
// The []byte must not be modified after conversion (the string
// borrows the underlying bytes). Used for MVCC chain lookup
// on the hot read path (REQ000605).
func bytesToString(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

type MV struct {
	chains sync.Map
}

func NewMV() *MV {
	return &MV{}
}

func (m *MV) GetVersionChain(key []byte) *VersionChain {
	chain, ok := m.chains.Load(bytesToString(key))
	if !ok {
		return nil
	}
	return chain.(*VersionChain)
}

func (m *MV) GetOrCreateVersionChain(key []byte) *VersionChain {
	chainI, _ := m.chains.LoadOrStore(bytesToString(key), &VersionChain{})
	return chainI.(*VersionChain)
}

func (m *MV) Insert(key []byte, node *VersionNode) bool {
	chain := m.GetOrCreateVersionChain(key)
	return chain.Insert(node)
}

func (m *MV) FindVisible(key []byte, readTS uint64) *VersionNode {
	chain := m.GetVersionChain(key)
	if chain == nil {
		return nil
	}
	return chain.FindVisible(readTS)
}

func (m *MV) Commit(node *VersionNode, commitTS uint64) bool {
	return node.Commit(commitTS)
}
