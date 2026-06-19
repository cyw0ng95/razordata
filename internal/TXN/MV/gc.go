package MV

// GCVersionChain walks a single version chain and returns the
// version nodes whose endTS is below oldestActiveReadTS — those
// versions are no longer visible to any in-flight transaction
// (REQ000551). Returns nil when the chain is empty or all nodes
// are still visible.
// Note: this function does NOT unlink nodes; it returns the
// candidates. The caller decides whether to unlink (e.g. only
// after pinning the chain).
func (m *MV) GCVersionChain(key []byte, oldestActiveReadTS uint64) []*VersionNode {
	chain := m.GetVersionChain(key)
	if chain == nil {
		return nil
	}
	return collectGC(chain, oldestActiveReadTS)
}

// GCAllChains walks every version chain and returns all nodes
// eligible for pruning (REQ000551).
func (m *MV) GCAllChains(oldestActiveReadTS uint64) []*VersionNode {
	var out []*VersionNode
	m.chains.Range(func(_, value any) bool {
		chain := value.(*VersionChain)
		out = append(out, collectGC(chain, oldestActiveReadTS)...)
		return true
	})
	return out
}

// NumChains returns the number of distinct version chains tracked
// by this MV (REQ000551). Useful for GC statistics and tests.
func (m *MV) NumChains() int {
	count := 0
	m.chains.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// collectGC walks a chain and appends pruned nodes (including the
// head) to the result. The caller decides whether to unlink.
func collectGC(vc *VersionChain, threshold uint64) []*VersionNode {
	var out []*VersionNode
	for cur := vc.GetHead(); cur != nil; cur = cur.next.Load() {
		endTS := cur.endTS.Load()
		if endTS != maxUint64 && endTS < threshold {
			out = append(out, cur)
		}
	}
	return out
}
