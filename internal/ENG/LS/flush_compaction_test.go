package ls

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFlushJob_Run(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_flush_job")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	mt := newMemtable(1024 * 1024)
	mt.Insert([]byte("key1"), []byte("value1"))
	mt.Insert([]byte("key2"), []byte("value2"))
	mt.Freeze()

	job := &flushJob{
		memtable:   mt,
		outputPath: filepath.Join(dir, "L0_1.sst"),
		manifest:   manifest,
		fileID:     1,
		level:      0,
	}

	if err := job.Run(); err != nil {
		t.Fatalf("flushJob.Run failed: %v", err)
	}

	if _, err := os.Stat(job.outputPath); err != nil {
		t.Fatalf("SST file not created: %v", err)
	}
}

func TestFlushJob_RunNotFrozen(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_flush_not_frozen")

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	mt := newMemtable(1024 * 1024)
	mt.Insert([]byte("key1"), []byte("value1"))

	job := &flushJob{
		memtable:   mt,
		outputPath: filepath.Join(dir, "L0_1.sst"),
		manifest:   manifest,
		fileID:     1,
		level:      0,
	}

	err = job.Run()
	if err != ErrMemtableNotFrozen {
		t.Fatalf("expected ErrMemtableNotFrozen, got %v", err)
	}
}

func TestFlushJob_flushToSST(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_flush_to_sst")

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	mt := newMemtable(1024 * 1024)
	mt.Insert([]byte("key1"), []byte("value1"))
	mt.Freeze()

	job := &flushJob{
		memtable:   mt,
		outputPath: filepath.Join(dir, "L0_1.sst"),
		manifest:   manifest,
		fileID:     1,
		level:      0,
	}

	data, err := job.flushToSST()
	if err != nil {
		t.Fatalf("flushToSST failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty SST data")
	}
}

func TestFlushJob_updateManifest(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_update_manifest")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	mt := newMemtable(1024 * 1024)
	mt.Insert([]byte("key1"), []byte("value1"))
	mt.Freeze()

	sstPath := filepath.Join(dir, "L0_1.sst")
	if err := os.WriteFile(sstPath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write SST file: %v", err)
	}

	job := &flushJob{
		memtable:   mt,
		outputPath: sstPath,
		manifest:   manifest,
		fileID:     1,
		level:      0,
	}

	if err := job.updateManifest(); err != nil {
		t.Fatalf("updateManifest failed: %v", err)
	}

	v := manifest.Current()
	if len(v.levels) != 1 || len(v.levels[0]) != 1 {
		t.Fatalf("expected 1 level with 1 file")
	}
}

func TestFlushManager_Get_v2(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_fm_get")

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	fm := newFlushManager(dir, 1024*1024, manifest)
	defer fm.Close()

	fm.Insert([]byte("key1"), []byte("value1"))

	val, found := fm.Get([]byte("key1"))
	if !found {
		t.Fatal("expected to find key1")
	}
	if string(val) != "value1" {
		t.Fatalf("expected value1, got %s", string(val))
	}

	_, found = fm.Get([]byte("nonexistent"))
	if found {
		t.Fatal("expected not to find nonexistent key")
	}
}

func TestFlushManager_Insert_v2(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_fm_insert")

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	fm := newFlushManager(dir, 1024*1024, manifest)
	defer fm.Close()

	err = fm.Insert([]byte("key1"), []byte("value1"))
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	_, found := fm.Get([]byte("key1"))
	if !found {
		t.Fatal("expected to find key1 after insert")
	}
}

func TestFlushManager_ActiveMemtable_v2(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_fm_active")

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	fm := newFlushManager(dir, 1024*1024, manifest)
	defer fm.Close()

	active := fm.ActiveMemtable()
	if active == nil {
		t.Fatal("expected non-nil active memtable")
	}
}

func TestFlushManager_Close_v2(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_fm_close")

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	fm := newFlushManager(dir, 1024*1024, manifest)

	if err := fm.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}

	if err := fm.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}

func TestFlushManager_QueueFlush(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_fm_queue")

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	fm := newFlushManager(dir, 1024*1024, manifest)
	defer fm.Close()

	mt := newMemtable(1024)
	mt.Insert([]byte("key1"), []byte("value1"))
	mt.Freeze()

	fm.requestFlush(mt)
}

func TestPrimaryIndex_Insert(t *testing.T) {
	idx := newPrimaryIndex(1, 1)

	idx.Insert([]byte("key1"), []byte("primary1"))
	idx.Insert([]byte("key2"), []byte("primary2"))

	primary, found := idx.Find([]byte("key1"))
	if !found {
		t.Fatal("expected to find key1")
	}
	if string(primary) != "primary1" {
		t.Fatalf("expected primary1, got %s", string(primary))
	}
}

func TestPrimaryIndex_Find(t *testing.T) {
	idx := newPrimaryIndex(1, 1)

	idx.Insert([]byte("key1"), []byte("primary1"))

	_, found := idx.Find([]byte("nonexistent"))
	if found {
		t.Fatal("expected not to find nonexistent key")
	}
}

func TestPrimaryIndex_Delete(t *testing.T) {
	idx := newPrimaryIndex(1, 1)

	idx.Insert([]byte("key1"), []byte("primary1"))
	idx.Insert([]byte("key2"), []byte("primary2"))

	deleted := idx.Delete([]byte("key1"))
	if !deleted {
		t.Fatal("expected delete to return true")
	}

	_, found := idx.Find([]byte("key1"))
	if found {
		t.Fatal("expected key1 to be deleted")
	}

	_, found = idx.Find([]byte("key2"))
	if !found {
		t.Fatal("expected key2 to still exist")
	}
}

func TestPrimaryIndex_Len(t *testing.T) {
	idx := newPrimaryIndex(1, 1)

	if idx.Len() != 0 {
		t.Fatalf("expected 0, got %d", idx.Len())
	}

	idx.Insert([]byte("key1"), []byte("primary1"))
	idx.Insert([]byte("key2"), []byte("primary2"))

	if idx.Len() != 2 {
		t.Fatalf("expected 2, got %d", idx.Len())
	}
}

func TestPKIterator_Next(t *testing.T) {
	idx := newPrimaryIndex(1, 1)
	idx.Insert([]byte("key1"), []byte("primary1"))
	idx.Insert([]byte("key2"), []byte("primary2"))

	it := idx.Iterator()
	count := 0
	for it.Next() {
		count++
	}

	if count != 2 {
		t.Fatalf("expected 2 iterations, got %d", count)
	}
}

func TestPKIterator_Key(t *testing.T) {
	idx := newPrimaryIndex(1, 1)
	idx.Insert([]byte("key1"), []byte("primary1"))

	it := idx.Iterator()
	if !it.Next() {
		t.Fatal("expected Next() to return true")
	}

	key := it.Key()
	if string(key) != "key1" {
		t.Fatalf("expected key1, got %s", string(key))
	}
}

func TestPKIterator_Primary(t *testing.T) {
	idx := newPrimaryIndex(1, 1)
	idx.Insert([]byte("key1"), []byte("primary1"))

	it := idx.Iterator()
	if !it.Next() {
		t.Fatal("expected Next() to return true")
	}

	primary := it.Primary()
	if string(primary) != "primary1" {
		t.Fatalf("expected primary1, got %s", string(primary))
	}
}

func TestPKIterator_Seek(t *testing.T) {
	idx := newPrimaryIndex(1, 1)
	idx.Insert([]byte("key1"), []byte("primary1"))

	it := idx.Iterator()
	result := it.Seek([]byte("key1"))
	if result {
		t.Fatal("expected Seek to return false (not implemented)")
	}
}

func TestRemoveFiles(t *testing.T) {
	files := []SSTFileMeta{
		{FileID: 1, MinKey: []byte("a"), MaxKey: []byte("c")},
		{FileID: 2, MinKey: []byte("d"), MaxKey: []byte("f")},
		{FileID: 3, MinKey: []byte("g"), MaxKey: []byte("i")},
	}

	toRemove := []SSTFileMeta{
		{FileID: 2},
	}

	result := removeFiles(files, toRemove)
	if len(result) != 2 {
		t.Fatalf("expected 2 files remaining, got %d", len(result))
	}
	if result[0].FileID != 1 || result[1].FileID != 3 {
		t.Fatal("wrong files remaining")
	}
}

func TestKeyHeap_Less(t *testing.T) {
	e1 := &SSTFileMeta{FileID: 1, MinKey: []byte("a"), MaxKey: []byte("c"), Size: 100}
	e2 := &SSTFileMeta{FileID: 2, MinKey: []byte("b"), MaxKey: []byte("d"), Size: 200}

	f1 := &sstIterator{}
	f2 := &sstIterator{}

	f1.current = 0
	f2.current = 0

	h := &keyHeap{items: []*sstIterator{f1, f2}}
	_ = e1
	_ = e2
	_ = h
}
