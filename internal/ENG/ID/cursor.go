package id

import (
	"bytes"
)

// Cursor provides ordered traversal of B-tree entries.
type Cursor struct {
	bt      *BTree
	path    []uint32 // stack of page IDs from root to current leaf
	indices []int    // corresponding child index at each level
	leafIdx int      // index within current leaf
	key     []byte
	value   []byte
	valid   bool
}

// Seek positions the cursor at the first entry >= key.
func (c *Cursor) Seek(key []byte) bool {
	c.bt.mu.RLock()
	defer c.bt.mu.RUnlock()
	if c.bt.root == 0 {
		return false
	}
	c.path = nil
	c.indices = nil
	c.valid = false
	c.seekTo(c.bt.root, key)
	return c.valid
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
func (c *Cursor) Next() bool {
	if !c.valid || len(c.path) == 0 {
		return false
	}
	c.bt.mu.RLock()
	defer c.bt.mu.RUnlock()

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

		// The current position in parent is parentIdx.
		// The separator key at parentIdx separates the left child
		// (which we just finished) from the right child.
		// We need to descend into parent.childs[parentIdx+1].
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
