package ls

import (
	"container/heap"
	"os"
	"path/filepath"
	"testing"
)

func TestFlushJob_Run(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_flush_job")

	if err := os.MkdirAll(dir, 0o755); err != nil {
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

	// R16-7: outputPath is now the SST subdirectory; the flush
	// job writes a temp file there and renames to fileName(meta).
	sstDir := filepath.Join(dir, "sst")
	job := &flushJob{
		fs:         DefaultFS(),
		memtable:   mt,
		outputPath: sstDir,
		manifest:   manifest,
		fileID:     1,
		level:      0,
	}

	if err := job.Run(); err != nil {
		t.Fatalf("flushJob.Run failed: %v", err)
	}

	// After the fix, the SST lands at dir/sst/L0_<minkey>_<maxkey>_<id>.sst
	v := manifest.Current()
	if len(v.levels) == 0 || len(v.levels[0]) == 0 {
		t.Fatalf("manifest has no L0 entries")
	}
	wantPath := filepath.Join(dir, fileName(&v.levels[0][0]))
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("R16-7: SST not at fileName(meta) path: %v (want %s)", err, wantPath)
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
		fs:         DefaultFS(),
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
		fs:         DefaultFS(),
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

	if err := os.MkdirAll(dir, 0o755); err != nil {
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

	// R16-7: write a temp SST in the sst/ subdir; updateManifest
	// renames it to fileName(meta).
	tmpPath := filepath.Join(dir, "sst", ".tmp_test.sst")
	if err := os.MkdirAll(filepath.Dir(tmpPath), 0o755); err != nil {
		t.Fatalf("mkdir sst: %v", err)
	}
	if err := os.WriteFile(tmpPath, []byte("test"), 0o644); err != nil {
		t.Fatalf("failed to write SST file: %v", err)
	}

	job := &flushJob{
		fs:         DefaultFS(),
		memtable:   mt,
		outputPath: tmpPath,
		manifest:   manifest,
		fileID:     1,
		level:      0,
	}

	if err := job.updateManifest(tmpPath); err != nil {
		t.Fatalf("updateManifest failed: %v", err)
	}

	v := manifest.Current()
	if len(v.levels) != 1 || len(v.levels[0]) != 1 {
		t.Fatalf("expected1 level with1 file")
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

	fm := newFlushManager(DefaultFS(), dir, 1024*1024, manifest)
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

	fm := newFlushManager(DefaultFS(), dir, 1024*1024, manifest)

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

	fm := newFlushManager(DefaultFS(), dir, 1024*1024, manifest)
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
	h := &keyHeap{items: []*sstIterator{}}

	if h.Len() != 0 {
		t.Fatalf("expected len=0, got %d", h.Len())
	}
}

func TestKeyHeap_PushPop(t *testing.T) {
	f1 := &sstIterator{pairs: []kvPair{{key: []byte("apple"), value: []byte("1")}}, pairIdx: 0}
	f2 := &sstIterator{pairs: []kvPair{{key: []byte("banana"), value: []byte("2")}}, pairIdx: 0}
	f3 := &sstIterator{pairs: []kvPair{{key: []byte("cherry"), value: []byte("3")}}, pairIdx: 0}

	h := &keyHeap{items: []*sstIterator{}}
	heap.Push(h, f2)
	heap.Push(h, f1)
	heap.Push(h, f3)

	if h.Len() != 3 {
		t.Fatalf("expected len=3, got %d", h.Len())
	}

	first := heap.Pop(h).(*sstIterator)
	if string(first.Key()) != "apple" {
		t.Errorf("expected first key=apple, got %s", first.Key())
	}

	second := heap.Pop(h).(*sstIterator)
	if string(second.Key()) != "banana" {
		t.Errorf("expected second key=banana, got %s", second.Key())
	}

	third := heap.Pop(h).(*sstIterator)
	if string(third.Key()) != "cherry" {
		t.Errorf("expected third key=cherry, got %s", third.Key())
	}
}

func TestKeyHeap_Swap(t *testing.T) {
	f1 := &sstIterator{pairs: []kvPair{{key: []byte("a"), value: []byte("1")}}, pairIdx: 0}
	f2 := &sstIterator{pairs: []kvPair{{key: []byte("b"), value: []byte("2")}}, pairIdx: 0}

	h := &keyHeap{items: []*sstIterator{f1, f2}}

	h.Swap(0, 1)

	if string(h.items[0].Key()) != "b" || string(h.items[1].Key()) != "a" {
		t.Error("Swap did not correctly swap items")
	}
}

func TestCompactionJob_RunNoFiles(t *testing.T) {
	dir := t.TempDir()

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	v := manifest.Current()
	v.levels = make([][]SSTFileMeta, 3)
	manifest.Apply(*v)

	job := &compactionJob{
		fs:              DefaultFS(),
		placementPolicy: nil,
		level:           0,
		inputs:          []SSTFileMeta{},
	}

	err = job.Run(manifest, dir)
	if err != ErrNoFilesToCompact {
		t.Fatalf("expected ErrNoFilesToCompact, got %v", err)
	}
}

func TestCompactionJob_RunWithOverlap(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_compaction_overlap")

	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("failed to create sst dir: %v", err)
	}

	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("failed to create manifest: %v", err)
	}
	defer manifest.Close()

	v := manifest.Current()
	v.levels = make([][]SSTFileMeta, 3)
	manifest.Apply(*v)

	w1 := newSSTWriter()
	w1.Add([]byte("key1"), []byte("value1"))
	sstData1, err := w1.Finish()
	if err != nil {
		t.Fatalf("failed to finish SST writer: %v", err)
	}

	w2 := newSSTWriter()
	w2.Add([]byte("key2"), []byte("value2"))
	sstData2, err := w2.Finish()
	if err != nil {
		t.Fatalf("failed to finish SST writer: %v", err)
	}

	sstPath1 := filepath.Join(dir, "sst", "L0_61_7a_1.sst")
	sstPath2 := filepath.Join(dir, "sst", "L0_62_79_2.sst")
	if err := os.WriteFile(sstPath1, sstData1, 0644); err != nil {
		t.Fatalf("failed to write SST file: %v", err)
	}
	if err := os.WriteFile(sstPath2, sstData2, 0644); err != nil {
		t.Fatalf("failed to write SST file: %v", err)
	}

	job := &compactionJob{
		fs:              DefaultFS(),
		placementPolicy: nil,
		level:           0,
		inputs: []SSTFileMeta{
			{FileID: 1, Level: 0, MinKey: []byte("a"), MaxKey: []byte("z"), Size: int64(len(sstData1)), BloomBits: 10},
		},
		overlap: []SSTFileMeta{
			{FileID: 2, Level: 0, MinKey: []byte("b"), MaxKey: []byte("y"), Size: int64(len(sstData2)), BloomBits: 10},
		},
	}

	if err := job.Run(manifest, dir); err != nil {
		t.Fatalf("compaction job with overlap failed: %v", err)
	}
}

func TestPkIteratorKeyPrimaryNil(t *testing.T) {
	it := &pkIterator{}

	if key := it.Key(); key != nil {
		t.Errorf("expected nil key for nil current, got %v", key)
	}

	if primary := it.Primary(); primary != nil {
		t.Errorf("expected nil primary for nil current, got %v", primary)
	}
}

func TestPkIteratorWithEntry(t *testing.T) {
	entry := &pkEntry{
		key:     []byte("testkey"),
		primary: []byte("testprimary"),
	}

	it := &pkIterator{
		current: entry,
	}

	if key := it.Key(); string(key) != "testkey" {
		t.Errorf("expected key=testkey, got %s", key)
	}

	if primary := it.Primary(); string(primary) != "testprimary" {
		t.Errorf("expected primary=testprimary, got %s", primary)
	}
}

func TestPkIteratorNext(t *testing.T) {
	head := &pkEntry{}
	nextEntry := &pkEntry{
		key:     []byte("key1"),
		primary: []byte("primary1"),
	}
	head.next.Store(nextEntry)

	it := &pkIterator{
		head: head,
	}

	if !it.Next() {
		t.Fatal("expected Next to return true")
	}

	if key := it.Key(); string(key) != "key1" {
		t.Errorf("expected key=key1, got %s", key)
	}

	if primary := it.Primary(); string(primary) != "primary1" {
		t.Errorf("expected primary=primary1, got %s", primary)
	}

	if it.Next() {
		t.Error("expected Next to return false at end")
	}
}
