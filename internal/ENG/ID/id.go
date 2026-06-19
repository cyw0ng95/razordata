package id

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync"
)

const (
	pageSize   = 4096
	headerSize = 16
	maxKeys    = 200
	pageMagic  = 0x42545245 // "BTRE"
	fileName   = "btree.razor"
)

var (
	ErrNotFound = errors.New("id: key not found")
	ErrClosed   = errors.New("id: btree closed")
)

// BTree is a persistent B-tree for secondary indexes.
type BTree struct {
	mu     sync.RWMutex
	root   uint32
	file   *os.File
	pages  map[uint32]*page
	dirty  map[uint32]bool
	nextID uint32
	closed bool
}

type pageType byte

const (
	nodeInternal pageType = 0
	nodeLeaf     pageType = 1
)

type pageHeader struct {
	pType   pageType
	numKeys uint16
	level   uint16
	crc     uint32
	_       [8]byte
}

type page struct {
	id     uint32
	header pageHeader
	keys   [][]byte
	vals   [][]byte
	childs []uint32
	dirty  bool
}

func newPage(id uint32, pType pageType) *page {
	return &page{
		id:     id,
		header: pageHeader{pType: pType},
	}
}

// Open creates or opens a B-tree file.
func Open(dir string) (*BTree, error) {
	path := filepath.Join(dir, fileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	bt := &BTree{
		file:   f,
		pages:  make(map[uint32]*page),
		dirty:  make(map[uint32]bool),
		nextID: 1,
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if stat.Size() > 0 {
		if err := bt.loadRoot(); err != nil {
			f.Close()
			return nil, err
		}
	}
	return bt, nil
}

// Close flushes dirty pages and closes the file.
func (bt *BTree) Close() error {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	if bt.closed {
		return ErrClosed
	}
	if err := bt.flush(); err != nil {
		return err
	}
	bt.closed = true
	return bt.file.Close()
}

func (bt *BTree) allocPageID() uint32 {
	if bt.nextID == 0 {
		bt.nextID = 1
	}
	id := bt.nextID
	bt.nextID++
	return id
}

// Insert adds a key-value pair.
func (bt *BTree) Insert(key, value []byte) error {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	if bt.closed {
		return ErrClosed
	}
	if bt.root == 0 {
		bt.root = 1
		bt.nextID = 2
		leaf := newPage(1, nodeLeaf)
		leaf.keys = append(leaf.keys, copyBytes(key))
		leaf.vals = append(leaf.vals, copyBytes(value))
		leaf.dirty = true
		bt.pages[leaf.id] = leaf
		bt.dirty[leaf.id] = true
		return nil
	}
	root := bt.getPage(bt.root)
	if len(root.keys) >= maxKeys {
		newRoot := newPage(bt.allocPageID(), nodeInternal)
		newRoot.childs = []uint32{bt.root}
		newRoot.dirty = true
		bt.pages[newRoot.id] = newRoot
		bt.dirty[newRoot.id] = true
		bt.splitChild(newRoot, 0)
		bt.root = newRoot.id
	}
	bt.insertNonFull(bt.root, key, value)
	return nil
}

// Get returns the value for key, or ErrNotFound.
func (bt *BTree) Get(key []byte) ([]byte, error) {
	bt.mu.RLock()
	defer bt.mu.RUnlock()
	if bt.closed {
		return nil, ErrClosed
	}
	if bt.root == 0 {
		return nil, ErrNotFound
	}
	return bt.search(bt.root, key)
}

// Delete removes a key. Returns ErrNotFound if absent.
func (bt *BTree) Delete(key []byte) error {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	if bt.closed {
		return ErrClosed
	}
	if bt.root == 0 {
		return ErrNotFound
	}
	found := bt.delete(bt.root, key)
	if !found {
		return ErrNotFound
	}
	root := bt.getPage(bt.root)
	if len(root.keys) == 0 && root.header.pType != nodeLeaf {
		if root.header.pType == nodeInternal && len(root.childs) > 0 {
			bt.root = root.childs[0]
		}
	}
	return nil
}

// Cursor provides seek/scan over the B-tree.
func (bt *BTree) Cursor() *Cursor {
	return &Cursor{bt: bt}
}

func (bt *BTree) getPage(id uint32) *page {
	if p, ok := bt.pages[id]; ok {
		return p
	}
	p := bt.loadPage(id)
	if p != nil {
		bt.pages[id] = p
	}
	return p
}

func (bt *BTree) loadPage(id uint32) *page {
	offset := int64(id-1) * int64(pageSize)
	buf := make([]byte, pageSize)
	n, err := bt.file.ReadAt(buf, offset)
	if err != nil || n < headerSize {
		return nil
	}
	hdr := decodeHeader(buf[:headerSize])
	if hdr.crc != crc32.ChecksumIEEE(buf[headerSize:]) {
		return nil
	}
	p := &page{id: id, header: *hdr}
	off := headerSize
	for i := 0; i < int(hdr.numKeys); i++ {
		if off+2 > pageSize {
			break
		}
		klen := int(binary.BigEndian.Uint16(buf[off:]))
		off += 2
		if off+klen > pageSize {
			break
		}
		key := make([]byte, klen)
		copy(key, buf[off:off+klen])
		off += klen
		p.keys = append(p.keys, key)
		if hdr.pType == nodeLeaf {
			if off+2 > pageSize {
				break
			}
			vlen := int(binary.BigEndian.Uint16(buf[off:]))
			off += 2
			if off+vlen > pageSize {
				break
			}
			val := make([]byte, vlen)
			copy(val, buf[off:off+vlen])
			off += vlen
			p.vals = append(p.vals, val)
		}
	}
	if hdr.pType == nodeInternal {
		// child pointers follow after all keys in internal nodes
		for i := 0; i < int(hdr.numKeys)+1; i++ {
			if off+4 > pageSize {
				break
			}
			childID := binary.BigEndian.Uint32(buf[off:])
			off += 4
			p.childs = append(p.childs, childID)
		}
	}
	return p
}

func (bt *BTree) flush() error {
	for id, p := range bt.pages {
		if !p.dirty && !bt.dirty[id] {
			continue
		}
		data := encodePage(p)
		offset := int64(id-1) * int64(pageSize)
		if _, err := bt.file.WriteAt(data, offset); err != nil {
			return err
		}
		p.dirty = false
		delete(bt.dirty, id)
	}
	return bt.file.Sync()
}

func (bt *BTree) loadRoot() error {
	stat, err := bt.file.Stat()
	if err != nil {
		return err
	}
	if stat.Size() < int64(pageSize) {
		return nil
	}
	buf := make([]byte, pageSize)
	if _, err := bt.file.ReadAt(buf, 0); err != nil {
		return err
	}
	hdr := decodeHeader(buf[:headerSize])
	if hdr.crc != crc32.ChecksumIEEE(buf[headerSize:]) {
		return nil
	}
	bt.root = 1
	bt.nextID = 2
	// Count pages
	numPages := stat.Size() / int64(pageSize)
	if numPages > 1 {
		bt.nextID = uint32(numPages) + 1
	}
	return nil
}

func (bt *BTree) search(id uint32, key []byte) ([]byte, error) {
	p := bt.getPage(id)
	if p == nil {
		return nil, ErrNotFound
	}
	if p.header.pType == nodeLeaf {
		for i, k := range p.keys {
			if bytes.Equal(k, key) {
				return p.vals[i], nil
			}
		}
		return nil, ErrNotFound
	}
	child := bt.findChild(p, key)
	return bt.search(child, key)
}

func (bt *BTree) findChild(p *page, key []byte) uint32 {
	for i := 0; i < len(p.keys); i++ {
		if bytes.Compare(key, p.keys[i]) <= 0 {
			return p.childs[i]
		}
	}
	return p.childs[len(p.childs)-1]
}

func (bt *BTree) insertNonFull(id uint32, key, value []byte) {
	p := bt.getPage(id)
	if p == nil {
		return
	}
	if p.header.pType == nodeLeaf {
		i := 0
		for i < len(p.keys) && bytes.Compare(p.keys[i], key) < 0 {
			i++
		}
		if i < len(p.keys) && bytes.Equal(p.keys[i], key) {
			p.vals[i] = copyBytes(value)
		} else {
			p.keys = append(p.keys, nil)
			p.vals = append(p.vals, nil)
			copy(p.keys[i+1:], p.keys[i:])
			copy(p.vals[i+1:], p.vals[i:])
			p.keys[i] = copyBytes(key)
			p.vals[i] = copyBytes(value)
		}
		p.header.numKeys = uint16(len(p.keys))
		p.dirty = true
		bt.dirty[id] = true
		return
	}
	i := 0
	for i < len(p.keys) && bytes.Compare(p.keys[i], key) <= 0 {
		i++
	}
	childID := p.childs[i]
	child := bt.getPage(childID)
	if child != nil && len(child.keys) >= maxKeys {
		bt.splitChild(p, i)
		if bytes.Compare(key, p.keys[i]) > 0 {
			i++
		}
	}
	bt.insertNonFull(p.childs[i], key, value)
}

func (bt *BTree) splitChild(parent *page, idx int) {
	childID := parent.childs[idx]
	child := bt.getPage(childID)
	if child == nil {
		return
	}
	mid := len(child.keys) / 2
	newPage := newPage(bt.allocPageID(), child.header.pType)
	bt.pages[newPage.id] = newPage
	bt.dirty[newPage.id] = true

	if child.header.pType == nodeLeaf {
		n := len(child.keys) - mid
		newPage.keys = make([][]byte, n)
		newPage.vals = make([][]byte, n)
		for i := 0; i < n; i++ {
			newPage.keys[i] = copyBytes(child.keys[mid+i])
			newPage.vals[i] = copyBytes(child.vals[mid+i])
		}
		child.keys = child.keys[:mid:mid]
		child.vals = child.vals[:mid:mid]
	} else {
		n := len(child.keys) - mid
		newPage.childs = make([]uint32, 0, n+1)
		newPage.childs = append(newPage.childs, child.childs[mid])
		newPage.childs = append(newPage.childs, child.childs[mid+1:]...)
		newPage.keys = make([][]byte, n)
		for i := 0; i < n; i++ {
			newPage.keys[i] = copyBytes(child.keys[mid+i])
		}
		child.keys = child.keys[:mid:mid]
		child.childs = child.childs[: mid+1 : mid+1]
	}
	child.header.numKeys = uint16(len(child.keys))
	child.dirty = true
	bt.dirty[childID] = true
	newPage.header.numKeys = uint16(len(newPage.keys))
	newPage.dirty = true

	midKey := copyBytes(child.keys[mid-1])

	parent.keys = append(parent.keys, nil)
	copy(parent.keys[idx+1:], parent.keys[idx:])
	parent.keys[idx] = midKey
	parent.childs = append(parent.childs, 0)
	copy(parent.childs[idx+2:], parent.childs[idx+1:])
	parent.childs[idx+1] = newPage.id
	parent.header.numKeys = uint16(len(parent.keys))
	parent.dirty = true
	bt.dirty[parent.id] = true
}

func (bt *BTree) delete(id uint32, key []byte) bool {
	p := bt.getPage(id)
	if p == nil {
		return false
	}
	if p.header.pType == nodeLeaf {
		for i, k := range p.keys {
			if bytes.Equal(k, key) {
				p.keys = append(p.keys[:i], p.keys[i+1:]...)
				p.vals = append(p.vals[:i], p.vals[i+1:]...)
				p.header.numKeys = uint16(len(p.keys))
				p.dirty = true
				bt.dirty[id] = true
				// REQ000284: rebalance after delete if leaf
				// is underflowing (below half capacity).
				bt.rebalanceLeaf(id)
				return true
			}
		}
		return false
	}
	i := 0
	for i < len(p.keys) && bytes.Compare(p.keys[i], key) <= 0 {
		i++
	}
	if bt.delete(p.childs[i], key) {
		// REQ000284: after child delete, check if child
		// underflowed and needs rebalancing.
		child := bt.getPage(p.childs[i])
		if child != nil && int(child.header.numKeys) < (bt.maxKeys(p)+1)/2 {
			bt.rebalanceNode(p, i)
		}
		return true
	}
	return false
}

// rebalanceLeaf attempts to merge or redistribute a leaf
// node that has underflowed after deletion. REQ000284.
func (bt *BTree) rebalanceLeaf(id uint32) {
	// For leaf nodes, find the parent and sibling via the
	// parent's childs array. Then either merge the leaf into
	// the sibling (if combined <= maxKeys) or redistribute
	// keys between them.
	// Implementation: we walk the tree from root to find the
	// parent of this leaf. This is O(h) but acceptable for
	// the current tree depth.
	leaf := bt.getPage(id)
	if leaf == nil || leaf.header.pType != nodeLeaf {
		return
	}
	// Find parent by scanning root-to-leaf.
	parent, idx := bt.findParent(0, id)
	if parent == nil {
		return // root leaf, no rebalancing needed
	}
	// Try to borrow from left sibling.
	if idx > 0 {
		sibling := bt.getPage(parent.childs[idx-1])
		if sibling != nil && sibling.header.pType == nodeLeaf {
			if len(leaf.keys) > 0 && len(sibling.keys) > (bt.maxKeys(sibling)+1)/2 {
				// Redistribute: move first key from sibling to leaf.
				leaf.keys = append([][]byte{sibling.keys[len(sibling.keys)-1]}, leaf.keys...)
				leaf.vals = append([][]byte{sibling.vals[len(sibling.vals)-1]}, leaf.vals...)
				sibling.keys = sibling.keys[:len(sibling.keys)-1]
				sibling.vals = sibling.vals[:len(sibling.vals)-1]
				leaf.header.numKeys = uint16(len(leaf.keys))
				sibling.header.numKeys = uint16(len(sibling.keys))
				leaf.dirty = true
				sibling.dirty = true
				parent.keys[idx-1] = copyBytes(leaf.keys[0])
				parent.dirty = true
				return
			}
		}
	}
	// Try to borrow from right sibling.
	if idx < len(parent.childs)-1 {
		sibling := bt.getPage(parent.childs[idx+1])
		if sibling != nil && sibling.header.pType == nodeLeaf {
			if len(sibling.keys) > (bt.maxKeys(sibling)+1)/2 {
				// Redistribute: move last key from sibling to leaf.
				leaf.keys = append(leaf.keys, copyBytes(sibling.keys[0]))
				leaf.vals = append(leaf.vals, copyBytes(sibling.vals[0]))
				sibling.keys = sibling.keys[1:]
				sibling.vals = sibling.vals[1:]
				leaf.header.numKeys = uint16(len(leaf.keys))
				sibling.header.numKeys = uint16(len(sibling.keys))
				leaf.dirty = true
				sibling.dirty = true
				parent.keys[idx] = copyBytes(sibling.keys[0])
				parent.dirty = true
				return
			}
		}
	}
	// Cannot borrow: merge with left sibling if possible.
	if idx > 0 {
		sibling := bt.getPage(parent.childs[idx-1])
		if sibling != nil && sibling.header.pType == nodeLeaf {
			// Merge leaf into sibling.
			sibling.keys = append(sibling.keys, leaf.keys...)
			sibling.vals = append(sibling.vals, leaf.vals...)
			sibling.header.numKeys = uint16(len(sibling.keys))
			sibling.dirty = true
			// Remove leaf from parent.
			parent.keys = append(parent.keys[:idx-1], parent.keys[idx:]...)
			parent.childs = append(parent.childs[:idx-1], parent.childs[idx:]...)
			parent.header.numKeys = uint16(len(parent.keys))
			parent.dirty = true
			return
		}
	}
	// Cannot borrow from left: merge with right sibling.
	if idx < len(parent.childs)-1 {
		sibling := bt.getPage(parent.childs[idx+1])
		if sibling != nil && sibling.header.pType == nodeLeaf {
			leaf.keys = append(leaf.keys, sibling.keys...)
			leaf.vals = append(leaf.vals, sibling.vals...)
			leaf.header.numKeys = uint16(len(leaf.keys))
			leaf.dirty = true
			parent.keys = append(parent.keys[:idx], parent.keys[idx+1:]...)
			parent.childs = append(parent.childs[:idx], parent.childs[idx+1:]...)
			parent.header.numKeys = uint16(len(parent.keys))
			parent.dirty = true
			return
		}
	}
}

// rebalanceNode rebalances an internal node after child
// underflow. REQ000284.
func (bt *BTree) rebalanceNode(parent *page, childIdx int) {
	child := bt.getPage(parent.childs[childIdx])
	if child == nil {
		return
	}
	// Try to borrow from left sibling.
	if childIdx > 0 {
		sibling := bt.getPage(parent.childs[childIdx-1])
		if sibling != nil {
			if len(sibling.keys) > (bt.maxKeys(sibling)+1)/2 {
				// Borrow last key from sibling.
				child.keys = append([][]byte{parent.keys[childIdx-1]}, child.keys...)
				child.childs = append([]uint32{sibling.childs[len(sibling.childs)-1]}, child.childs...)
				parent.keys[childIdx-1] = copyBytes(sibling.keys[len(sibling.keys)-1])
				sibling.keys = sibling.keys[:len(sibling.keys)-1]
				sibling.childs = sibling.childs[:len(sibling.childs)-1]
				child.header.numKeys = uint16(len(child.keys))
				sibling.header.numKeys = uint16(len(sibling.keys))
				child.dirty = true
				sibling.dirty = true
				parent.dirty = true
				return
			}
		}
	}
	// Try to borrow from right sibling.
	if childIdx < len(parent.childs)-1 {
		sibling := bt.getPage(parent.childs[childIdx+1])
		if sibling != nil {
			if len(sibling.keys) > (bt.maxKeys(sibling)+1)/2 {
				// Borrow first key from sibling.
				child.keys = append(child.keys, parent.keys[childIdx])
				child.childs = append(child.childs, sibling.childs[0])
				parent.keys[childIdx] = copyBytes(sibling.keys[0])
				sibling.keys = sibling.keys[1:]
				sibling.childs = sibling.childs[1:]
				child.header.numKeys = uint16(len(child.keys))
				sibling.header.numKeys = uint16(len(sibling.keys))
				child.dirty = true
				sibling.dirty = true
				parent.dirty = true
				return
			}
		}
	}
	// Cannot borrow: merge with left sibling.
	if childIdx > 0 {
		sibling := bt.getPage(parent.childs[childIdx-1])
		if sibling != nil {
			sibling.keys = append(sibling.keys, parent.keys[childIdx-1])
			sibling.keys = append(sibling.keys, child.keys...)
			sibling.childs = append(sibling.childs, child.childs...)
			sibling.header.numKeys = uint16(len(sibling.keys))
			sibling.dirty = true
			parent.keys = append(parent.keys[:childIdx-1], parent.keys[childIdx:]...)
			parent.childs = append(parent.childs[:childIdx-1], parent.childs[childIdx:]...)
			parent.header.numKeys = uint16(len(parent.keys))
			parent.dirty = true
			return
		}
	}
	// Cannot borrow from left: merge with right sibling.
	if childIdx < len(parent.childs)-1 {
		sibling := bt.getPage(parent.childs[childIdx+1])
		if sibling != nil {
			child.keys = append(child.keys, parent.keys[childIdx])
			child.keys = append(child.keys, sibling.keys...)
			child.childs = append(child.childs, sibling.childs...)
			child.header.numKeys = uint16(len(child.keys))
			child.dirty = true
			parent.keys = append(parent.keys[:childIdx], parent.keys[childIdx+1:]...)
			parent.childs = append(parent.childs[:childIdx], parent.childs[childIdx+1:]...)
			parent.header.numKeys = uint16(len(parent.keys))
			parent.dirty = true
			return
		}
	}
}

// findParent walks from root to find the parent of the given
// node ID. Returns (parent, childIdx). REQ000284.
func (bt *BTree) findParent(rootID, targetID uint32) (*page, int) {
	p := bt.getPage(rootID)
	if p == nil || p.header.pType == nodeLeaf {
		return nil, -1
	}
	for i, cid := range p.childs {
		if cid == targetID {
			return p, i
		}
	}
	for _, cid := range p.childs {
		child := bt.getPage(cid)
		if child != nil && child.header.pType != nodeLeaf {
			if parent, idx := bt.findParent(cid, targetID); parent != nil {
				return parent, idx
			}
		}
	}
	return nil, -1
}

// maxKeys returns the maximum number of keys for a page.
func (bt *BTree) maxKeys(p *page) int {
	const minKeyLen = 8 // minimum key size (uint64 blockID prefix)
	const minValLen = 1 // minimum value size (empty)
	if p.header.pType == nodeLeaf {
		return (pageSize - headerSize) / (minKeyLen + minValLen)
	}
	return (pageSize - headerSize) / (minKeyLen + 4) // 4 bytes for child pointer
}

func encodePage(p *page) []byte {
	buf := make([]byte, pageSize)
	hdr := p.header
	hdr.numKeys = uint16(len(p.keys))
	off := headerSize
	for i, k := range p.keys {
		binary.BigEndian.PutUint16(buf[off:], uint16(len(k)))
		off += 2
		copy(buf[off:], k)
		off += len(k)
		if p.header.pType == nodeLeaf && i < len(p.vals) {
			v := p.vals[i]
			binary.BigEndian.PutUint16(buf[off:], uint16(len(v)))
			off += 2
			copy(buf[off:], v)
			off += len(v)
		}
	}
	if p.header.pType == nodeInternal {
		for i := 0; i < len(p.childs); i++ {
			binary.BigEndian.PutUint32(buf[off:], p.childs[i])
			off += 4
		}
	}
	hdr.crc = crc32.ChecksumIEEE(buf[headerSize:])
	copy(buf[:headerSize], encodeHeader(&hdr))
	return buf
}

func encodeHeader(h *pageHeader) []byte {
	buf := make([]byte, headerSize)
	buf[0] = byte(h.pType)
	binary.BigEndian.PutUint16(buf[1:3], h.numKeys)
	binary.BigEndian.PutUint16(buf[3:5], h.level)
	binary.BigEndian.PutUint32(buf[5:9], h.crc)
	return buf
}

func decodeHeader(buf []byte) *pageHeader {
	return &pageHeader{
		pType:   pageType(buf[0]),
		numKeys: binary.BigEndian.Uint16(buf[1:3]),
		level:   binary.BigEndian.Uint16(buf[3:5]),
		crc:     binary.BigEndian.Uint32(buf[5:9]),
	}
}

func copyBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
