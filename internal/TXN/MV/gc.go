package MV

import "unsafe"

// GCVersionChain walks the version chain for key and returns pointers to
// version nodes whose endTS is strictly less than oldestReadTS. A version
// with endTS < oldestReadTS cannot be visible to any reader with
// readTS > oldestReadTS, since visibility requires endTS >= readTS and
// readTS > oldestReadTS > endTS is a contradiction.
//
// The chain itself is not modified — the caller is responsible for handing
// the returned pointers to a reclamation mechanism (e.g., hazard pointers
// in TXN/LC) so that readers still traversing the chain remain safe. Only
// once no reader holds a hazard on a node should its memory actually be
// released.
//
// Returns nil if the key has no chain or no candidates are found.
func (m *MV) GCVersionChain(key []byte, oldestReadTS uint64) []unsafe.Pointer {
	chain := m.GetVersionChain(key)
	if chain == nil {
		return nil
	}
	var candidates []unsafe.Pointer
	for node := chain.GetHead(); node != nil; node = node.Next() {
		if node.EndTS() < oldestReadTS {
			candidates = append(candidates, unsafe.Pointer(node))
		}
	}
	return candidates
}

// GCAllChains sweeps every chain in the MV and returns all version nodes
// whose endTS is strictly less than oldestReadTS. Useful for background
// reclamation sweeps.
//
// The chain is not modified; see GCVersionChain for reclamation contract.
func (m *MV) GCAllChains(oldestReadTS uint64) []unsafe.Pointer {
	var all []unsafe.Pointer
	m.chains.Range(func(_, value any) bool {
		chain := value.(*VersionChain)
		for node := chain.GetHead(); node != nil; node = node.Next() {
			if node.EndTS() < oldestReadTS {
				all = append(all, unsafe.Pointer(node))
			}
		}
		return true
	})
	return all
}

// NumChains returns the number of distinct keys currently tracked by the MV.
// Intended for tests and observability.
func (m *MV) NumChains() int {
	n := 0
	m.chains.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}
