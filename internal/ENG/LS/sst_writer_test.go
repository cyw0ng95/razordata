package ls

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestSSTWriterBasic(t *testing.T) {
	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))
	w.Add([]byte("key2"), []byte("value2"))

	if w.keyCount != 2 {
		t.Errorf("expected keyCount=2, got %d", w.keyCount)
	}

	if !bytes.Equal(w.minKey, []byte("key1")) {
		t.Errorf("expected minKey=key1, got %s", w.minKey)
	}

	if !bytes.Equal(w.maxKey, []byte("key2")) {
		t.Errorf("expected maxKey=key2, got %s", w.maxKey)
	}
}

func TestSSTWriterFinish(t *testing.T) {
	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))
	w.Add([]byte("key2"), []byte("value2"))

	data, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	if len(data) == 0 {
		t.Error("expected non-empty SST data")
	}
}

func TestSSTWriterEmpty(t *testing.T) {
	w := newSSTWriter()
	data, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}
	if data != nil {
		t.Error("expected nil for empty writer")
	}
}

func TestSSTWriterMultipleBlocks(t *testing.T) {
	w := newSSTWriter()

	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		val := []byte{byte(i + 100)}
		w.Add(key, val)
	}

	data, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	if len(data) == 0 {
		t.Error("expected non-empty SST data")
	}

	if w.keyCount != 100 {
		t.Errorf("expected keyCount=100, got %d", w.keyCount)
	}
}

func TestSSTWriterNotFound(t *testing.T) {
	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))

	data, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	if len(data) == 0 {
		t.Error("expected non-empty SST data")
	}
}

func TestSSTWriterReset(t *testing.T) {
	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))

	w.Reset()

	if w.keyCount != 0 {
		t.Errorf("expected keyCount=0 after reset, got %d", w.keyCount)
	}
	if len(w.blocks) != 0 {
		t.Errorf("expected empty blocks after reset, got %d", len(w.blocks))
	}
}

func TestSSTWriterMinMaxKey(t *testing.T) {
	w := newSSTWriter()

	if w.minKey != nil || w.maxKey != nil {
		t.Error("expected nil initial min/max keys")
	}

	w.Add([]byte("middle"), []byte("value"))

	if !bytes.Equal(w.minKey, []byte("middle")) || !bytes.Equal(w.maxKey, []byte("middle")) {
		t.Error("expected minKey=maxKey=middle after single add")
	}

	w.Add([]byte("a"), []byte("value_a"))
	w.Add([]byte("z"), []byte("value_z"))

	if !bytes.Equal(w.minKey, []byte("a")) {
		t.Errorf("expected minKey=a, got %s", w.minKey)
	}
	if !bytes.Equal(w.maxKey, []byte("z")) {
		t.Errorf("expected maxKey=z, got %s", w.maxKey)
	}
}

// TestBloomSizeFor verifies the dynamic sizing math (10 bits/key per
// ENG.md:91-92). (R16-9)
func TestBloomSizeFor(t *testing.T) {
	cases := []struct {
		keyCount  int
		wantBytes int
	}{
		{0, 1},       // empty: at least1 byte so footer is valid
		{1, 2},       // (1*10+7)/8 =2
		{8, 10},      // (8*10+7)/8 =10
		{10, 13},     // (10*10+7)/8 =12, ceiling =>13
		{100, 125},   // (100*10+7)/8 =125
		{1000, 1250}, // (1000*10+7)/8 =1250
		{4096, 5120}, // (4096*10+7)/8 =5120
	}
	for _, c := range cases {
		got := bloomSizeFor(c.keyCount)
		if got != c.wantBytes {
			t.Errorf("bloomSizeFor(%d) = %d, want %d", c.keyCount, got, c.wantBytes)
		}
	}
}

// TestSSTWriterBloomSizeScalesWithKeyCount verifies that the bloom
// filter embedded in the finished SST is sized dynamically from
// keyCount, not the fixed4096-byte default the pre-R16-9 code used.
// We write a small SST (5 keys, expected ~7 bytes) and a larger SST
// (200 keys, expected ~250 bytes) and assert the bloom sizes differ
// from the old default of4096 in the right direction. (R16-9)
func TestSSTWriterBloomSizeScalesWithKeyCount(t *testing.T) {
	cases := []struct {
		name        string
		keyCount    int
		wantAtLeast int
		wantAtMost  int
	}{
		{"five_keys", 5, 7, 7},              // (5*10+7)/8 =7
		{"two_hundred_keys", 200, 250, 250}, // (200*10+7)/8 =250
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newSSTWriter()
			for i := 0; i < c.keyCount; i++ {
				key := []byte{byte(i + 1), byte(i*7 + 3)}
				val := []byte{byte(i + 100)}
				w.Add(key, val)
			}
			data, err := w.Finish()
			if err != nil {
				t.Fatalf("Finish: %v", err)
			}
			if len(data) < sstFooterSize {
				t.Fatalf("SST too small to contain footer: %d bytes", len(data))
			}
			footerStart := len(data) - sstFooterSize
			bloomSize := binary.LittleEndian.Uint32(data[footerStart+20:])
			if int(bloomSize) < c.wantAtLeast {
				t.Errorf("bloom size = %d, want >= %d (keyCount=%d)",
					bloomSize, c.wantAtLeast, c.keyCount)
			}
			if int(bloomSize) > c.wantAtMost {
				t.Errorf("bloom size = %d, want <= %d (keyCount=%d)",
					bloomSize, c.wantAtMost, c.keyCount)
			}
			// Crucially: small SSTs should NOT be padded up to the
			// legacy4096-byte default.
			if bloomSize > 4096 && c.keyCount < 410 {
				t.Errorf("bloom size %d exceeds dynamic formula for %d keys",
					bloomSize, c.keyCount)
			}
		})
	}
}

// TestSSTWriterBloomFalsePositivesReasonable verifies that the
// dynamic bloom filter accepts every key it was sized for (true
// negatives are impossible; the bloom filter is a probabilistic
// superset of the key set). For100 keys with a ~125 byte filter
// (10 bits/key), the false-positive rate is ~1%. We assert that a
// random query for a key NOT in the writer's set has <50% chance of
// returning mayContain=true over1000 trials — generous bound that
// still catches a broken filter (e.g. all bits always zero). (R16-9)
func TestSSTWriterBloomFalsePositivesReasonable(t *testing.T) {
	w := newSSTWriter()
	inserted := make(map[string]bool)
	for i := 0; i < 100; i++ {
		key := []byte{byte(i), byte(i * 13), byte(i*7 + 5)}
		inserted[string(key)] = true
		w.Add(key, []byte("v"))
	}
	data, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	r, err := openSST(data)
	if err != nil {
		t.Fatalf("openSST: %v", err)
	}
	// All inserted keys must be reported as "may contain".
	for k := range inserted {
		if !r.mayContain([]byte(k)) {
			t.Errorf("inserted key %q reported absent", k)
		}
	}
	// Random non-inserted keys: false-positive rate should be small.
	hits := 0
	for i := 0; i < 1000; i++ {
		key := []byte{byte(i + 200), byte(i*31 + 1)}
		if _, ok := inserted[string(key)]; ok {
			continue
		}
		if r.mayContain(key) {
			hits++
		}
	}
	if hits > 100 {
		t.Errorf("false positive rate too high: %d/1000", hits)
	}
}
