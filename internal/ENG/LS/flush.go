package ls

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

var (
	ErrMemtableNotFrozen = errors.New("memtable is not frozen")
	ErrFlushInProgress   = errors.New("flush already in progress")
)

type flushJob struct {
	memtable   *memtable
	outputPath string
	manifest   *manifest
	fileID     uint64
	level      int
}

func (fj *flushJob) Run() error {
	if !fj.memtable.IsFrozen() {
		return ErrMemtableNotFrozen
	}

	sstData, err := fj.flushToSST()
	if err != nil {
		return err
	}

	tmpPath := fj.outputPath + ".tmp"
	if err := os.WriteFile(tmpPath, sstData, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, fj.outputPath); err != nil {
		return err
	}

	if err := fj.updateManifest(); err != nil {
		return err
	}

	return nil
}

func (fj *flushJob) flushToSST() ([]byte, error) {
	w := newSSTWriter()

	it := fj.memtable.Iterator()
	for it.Next() {
		w.Add(it.Key(), it.Value())
	}

	data, err := w.Finish()
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (fj *flushJob) updateManifest() error {
	stat, err := os.Stat(fj.outputPath)
	if err != nil {
		return err
	}

	it := fj.memtable.Iterator()
	var minKey, maxKey []byte
	var keyCount int64
	for it.Next() {
		if minKey == nil {
			minKey = it.Key()
		}
		maxKey = it.Key()
		keyCount++
	}

	if keyCount == 0 {
		return nil
	}

	files := []SSTFileMeta{{
		FileID:    fj.fileID,
		Level:     fj.level,
		MinKey:    minKey,
		MaxKey:    maxKey,
		Size:      stat.Size(),
		BloomBits: 10,
	}}

	current := fj.manifest.Current()
	newLevels := make([][]SSTFileMeta, len(current.levels)+1)
	for i, level := range current.levels {
		newLevels[i] = level
	}
	newLevels[len(current.levels)] = files

	v := Version{
		num:     current.num + 1,
		levels:  newLevels,
		created: time.Now(),
	}

	return fj.manifest.Apply(v)
}

type flushManager struct {
	memtables       []*memtable
	activeMemtable  atomic.Pointer[memtable]
	frozenMemtables []*memtable
	manifest       *manifest
	dir             string
	maxMemSize      int64
	flushQueue      chan *flushJob
	done            chan struct{}
}

func newFlushManager(dir string, maxMemSize int64, manifest *manifest) *flushManager {
	fm := &flushManager{
		dir:        dir,
		maxMemSize: maxMemSize,
		manifest:   manifest,
		flushQueue: make(chan *flushJob, 10),
		done:       make(chan struct{}),
	}

	active := newMemtable(maxMemSize)
	fm.activeMemtable.Store(active)

	go fm.flushLoop()

	return fm
}

func (fm *flushManager) flushLoop() {
	for {
		select {
		case <-fm.done:
			return
		case job := <-fm.flushQueue:
			if err := job.Run(); err != nil {
			}
		}
	}
}

func (fm *flushManager) MaybeFlush() {
	active := fm.activeMemtable.Load()
	if active.ShouldFlush() {
		fm.requestFlush(active)
	}
}

func (fm *flushManager) requestFlush(m *memtable) {
	m.Freeze()

	select {
	case fm.flushQueue <- &flushJob{
		memtable:   m,
		outputPath: filepath.Join(fm.dir, fmt.Sprintf("L0_%d.sst", nextFileID())),
		manifest:   fm.manifest,
		fileID:     nextFileID(),
		level:      0,
	}:
	default:
	}
}

func (fm *flushManager) ActiveMemtable() *memtable {
	return fm.activeMemtable.Load()
}

func (fm *flushManager) Get(key []byte) ([]byte, bool) {
	active := fm.activeMemtable.Load()
	if val, found := active.Get(key); found {
		return val, found
	}

	for _, m := range fm.frozenMemtables {
		if val, found := m.Get(key); found {
			return val, found
		}
	}

	return nil, false
}

func (fm *flushManager) Insert(key, value []byte) error {
	active := fm.activeMemtable.Load()
	if err := active.Insert(key, value); err != nil {
		return err
	}

	if active.ShouldFlush() {
		fm.MaybeFlush()
	}

	return nil
}

func (fm *flushManager) Close() error {
	close(fm.done)
	return nil
}

var fileIDCounter uint64

func nextFileID() uint64 {
	return atomic.AddUint64(&fileIDCounter, 1)
}