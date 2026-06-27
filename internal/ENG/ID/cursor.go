package id

import (
	"bytes"
)

// Cursor provides ordered traversal of B-tree entries.
// The cursor holds bt.mu.RLock across the entire traversal
// (from Seek to Close), providing a consistent snapshot view.
// REQ000975: added Close() and per-cursor lock ownership.
type Cursor struct {
	bt      *BTree
	held    bool     // if true, bt.mu.RLock is held by this cursor
	path    []uint32 // stack of page IDs from root to current leaf
	indices []int    // corresponding child index at each level
	leafIdx int      // index within current leaf
	key     []byte
	value   []byte
	valid   bool
}

// Seek positions the cursor at the first entry >= key.
// Acquires bt.mu.RLock; the lock is released by Close().
func (c *Cursor) Seek(key []byte) bool {
	if c.held {
		c.bt.mu.RUnlock()
		c.held = false
	}
	c.bt.mu.RLock()
	c.held = true
	if c.bt.root == 0 {
		c.bt.mu.RUnlock()
		c.held = false
		return false
	}
	c.path = nil
	c.indices = nil
	c.valid = false
	c.seekTo(c.bt.root, key)
	if !c.valid {
		c.bt.mu.RUnlock()
		c.held = false
		return false
	}
	return true
}

func (c *Cursor) seekTo(id uint32, key []byte) {
	p := c.bt.getPage(id)
	if p == nil {
		return
	}
	c.path = append(c.path, id)
	if p.header.pType == nodeLeaf {
		for i, k := range p.keys {
			if bytes.Compare(k, key) >= 0 {
				c.indices = append(c.indices, i)
				c.leafIdx = i
				c.key = k
				c.value = p.vals[i]
				c.valid = true
				return
			}
		}
		c.valid = false
		return
	}
	for i, k := range p.keys {
		if bytes.Compare(key, k) <= 0 {
			c.indices = append(c.indices, i)
			c.seekTo(p.childs[i], key)
			return
		}
	}
	c.indices = append(c.indices, len(p.keys))
	c.seekTo(p.childs[len(p.childs)-1], key)
}

// Next advances the cursor to the next entry in order.
// The bt.mu.RLock is already held from Seek().
// When the scan reaches the end, the lock is released automatically.
func (c *Cursor) Next() bool {
	if !c.valid || len(c.path) == 0 {
		if c.held {
			c.bt.mu.RUnlock()
			c.held = false
		}
		return false
	}

	// Try advancing within current leaf
	leafID := c.path[len(c.path)-1]
	leaf := c.bt.getPage(leafID)
	if leaf != nil && c.leafIdx+1 < len(leaf.keys) {
		c.leafIdx++
		c.key = leaf.keys[c.leafIdx]
		c.value = leaf.vals[c.leafIdx]
		return true
	}

	// Walk up the tree to find the next leaf
	for len(c.path) > 1 {
		// Pop current leaf
		c.path = c.path[:len(c.path)-1]
		c.indices = c.indices[:len(c.indices)-1]

		parentID := c.path[len(c.path)-1]
		parentIdx := c.indices[len(c.indices)-1]
		parent := c.bt.getPage(parentID)
		if parent == nil {
			break
		}

		childIdx := parentIdx + 1
		if childIdx >= len(parent.childs) {
			continue
		}

		// Descend into the right sibling
		c.indices[len(c.indices)-1] = childIdx
		childID := parent.childs[childIdx]
		c.descendLeftmost(childID)
		if c.valid {
			return true
		}
	}

	c.valid = false
	if c.held {
		c.bt.mu.RUnlock()
		c.held = false
	}
	return false
}

// descendLeftmost follows the leftmost path from the given page to a leaf.
func (c *Cursor) descendLeftmost(id uint32) {
	p := c.bt.getPage(id)
	if p == nil {
		return
	}
	c.path = append(c.path, id)
	if p.header.pType == nodeLeaf {
		if len(p.keys) > 0 {
			c.indices = append(c.indices, 0)
			c.leafIdx = 0
			c.key = p.keys[0]
			c.value = p.vals[0]
			c.valid = true
		} else {
			c.valid = false
		}
		return
	}
	c.indices = append(c.indices, 0)
	c.descendLeftmost(p.childs[0])
}

func (c *Cursor) Key() []byte {
	return c.key
}

func (c *Cursor) Value() []byte {
	return c.value
}

func (c *Cursor) Valid() bool {
	return c.valid
}

// Close releases the bt.mu.RLock held by this cursor.
// Safe to call multiple times. Satisfies RangeIter interface.
func (c *Cursor) Close() error {
	if c.held && c.bt != nil {
		c.bt.mu.RUnlock()
		c.held = false
	}
	c.valid = false
	c.path = nil
	c.indices = nil
	return nil
}
