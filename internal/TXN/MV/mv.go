package MV

import (
	"sync"
)

type MV struct {
	chains sync.Map
}

func NewMV() *MV {
	return &MV{}
}

func (m *MV) GetVersionChain(key []byte) *VersionChain {
	chain, ok := m.chains.Load(string(key))
	if !ok {
		return nil
	}
	return chain.(*VersionChain)
}

func (m *MV) GetOrCreateVersionChain(key []byte) *VersionChain {
	chainI, _ := m.chains.LoadOrStore(string(key), &VersionChain{})
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
