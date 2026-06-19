package ls

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrNoActiveMemtable = errors.New("no active memtable")
)

type ReadStats struct {
	MemtableHits int
	SSTHits      int
	DiskReads    int
}

type engine struct {
	dir       string
	memtables []*memtable
	activeMem *memtable
	manifest  *manifest
	cm        *compactionManager
	fm        *flushManager
	stats     ReadStats
	statsMu   sync.RWMutex
	mu        sync.RWMutex // REQ000574: guards memtables/activeMem
}

func newEngine(dir string) (*engine, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	sstDir := filepath.Join(dir, "sst")
	if err := os.MkdirAll(sstDir, 0755); err != nil {
		return nil, err
	}

	manifest, err := newManifest(dir)
	if err != nil {
		return nil, err
	}

	activeMem := newMemtable(64 * 1024 * 1024)

	e := &engine{
		dir:       dir,
		memtables: []*memtable{activeMem},
		activeMem: activeMem,
		manifest:  manifest,
	}

	e.cm = newCompactionManager(dir, manifest)
	e.fm = newFlushManager(dir, 64*1024*1024, manifest)

	return e, nil
}

func (e *engine) Write(key, value []byte) error {
	if e.activeMem == nil {
		return ErrNoActiveMemtable
	}

	if err := e.activeMem.Insert(key, value); err != nil {
		return err
	}

	if e.activeMem.Size() >= e.activeMem.maxSize {
		return e.flushActiveMemtable()
	}

	return nil
}

func (e *engine) flushActiveMemtable() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	frozen := e.activeMem
	frozen.Freeze()

	newMem := newMemtable(64 * 1024 * 1024)
	e.memtables = append(e.memtables, newMem)
	e.activeMem = newMem

	e.fm.requestFlush(frozen)

	idx := len(e.memtables) - 2
	e.memtables = append(e.memtables[:idx], e.memtables[idx+1:]...)

	return nil
}

// Sync flushes the active memtable to SST and blocks until complete.
func (e *engine) Sync() error {
	if e.activeMem == nil {
		return ErrNoActiveMemtable
	}
	if e.activeMem.Size() == 0 {
		return nil
	}
	if err := e.flushActiveMemtable(); err != nil {
		return err
	}
	e.fm.WaitForFlush()
	if p := e.fm.lastErr.Load(); p != nil {
		return *p
	}
	return nil
}

func (e *engine) Read(key []byte) ([]byte, error) {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()

	e.mu.RLock()
	defer e.mu.RUnlock()

	for i := len(e.memtables) - 1; i >= 0; i-- {
		mt := e.memtables[i]
		if val, found := mt.Get(key); found {
			e.stats.MemtableHits++
			return val, nil
		}
	}

	val, err := e.readFromSST(key)
	if err == nil {
		e.stats.SSTHits++
		return val, nil
	}
	return nil, ErrNotFound
}

func (e *engine) readFromSST(key []byte) ([]byte, error) {
	v := e.manifest.Current()

	for level := 0; level < len(v.levels); level++ {
		files := v.levels[level]

		for i := len(files) - 1; i >= 0; i-- {
			file := files[i]

			if bytes.Compare(key, file.MinKey) < 0 || bytes.Compare(key, file.MaxKey) > 0 {
				continue
			}

			e.stats.DiskReads++

			sstPath := filepath.Join(e.dir, fileName(&file))
			data, err := os.ReadFile(sstPath)
			if err != nil {
				continue
			}

			reader, err := openSST(data)
			if err != nil {
				continue
			}

			if !reader.mayContain(key) {
				continue
			}

			if val, found := reader.Find(key); found {
				return val, nil
			}
		}
	}

	return nil, ErrNotFound
}

func (e *engine) MayContain(key []byte) bool {
	v := e.manifest.Current()

	for _, mt := range e.memtables {
		if _, found := mt.Get(key); found {
			return true
		}
	}

	for level := 0; level < len(v.levels); level++ {
		files := v.levels[level]
		for i := range files {
			file := &files[i]
			sstPath := filepath.Join(e.dir, fileName(file))
			data, err := os.ReadFile(sstPath)
			if err != nil {
				continue
			}
			reader, err := openSST(data)
			if err != nil {
				continue
			}
			if reader.mayContain(key) {
				return true
			}
		}
	}

	return false
}

func (e *engine) GetStats() ReadStats {
	e.statsMu.RLock()
	defer e.statsMu.RUnlock()
	return e.stats
}

func (e *engine) Close() error {
	if e.cm == nil {
		return nil
	}
	if err := e.cm.Close(); err != nil {
		return err
	}
	if err := e.fm.Close(); err != nil {
		return err
	}
	e.cm = nil
	e.fm = nil
	if err := e.manifest.Close(); err != nil {
		return err
	}
	return nil
}
