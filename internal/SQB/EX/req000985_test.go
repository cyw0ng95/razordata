package EX

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"runtime"
	"testing"
)

// TestStore_EncodeRowNoAlloc verifies REQ000985: EncodeRow uses a pooled
// scratch buffer so the hot path does not allocate per call.
// The returned []byte is always a fresh copy (caller-owned), but the
// encoding itself reuses a sync.Pool buffer internally.
func TestStore_EncodeRowNoAlloc(t *testing.T) {
	_ = DT.RegisterStoreSchema("enc_test", []string{"id", "name", "val"}, "id")
	ss, _ := DT.SchemaFor("enc_test")
	row := DT.Row{
		Data: []DT.Value{
			NewIntValue(42),
			NewTextValue("hello"),
			NewFloatValue(3.14),
		},
	}

	// Warm up the pool.
	for i := 0; i < 10; i++ {
		_, _ = OP.EncodeRow(ss, row)
	}

	runtime.GC()
	runtime.GC()

	// REQ000985: the encoding path itself must not allocate.
	// The returned []byte is a copy (1 allocation per call) — that's
	// expected and unavoidable since the caller must own the data.
	// We verify the *encoding* does not allocate beyond that one copy.
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = OP.EncodeRow(ss, row)
	})

	// Exactly 1 allocation per call is the copy for the returned slice.
	// Any more means the encoding path is allocating (violates REQ000985).
	if allocs > 1 {
		t.Errorf("EncodeRow allocated %.1f allocs/call, want <= 1", allocs)
	}
}
