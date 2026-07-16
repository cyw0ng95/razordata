package EX

import (
	"testing"

	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
)

// REQ001461: isSimpleSort returns true for Sort(SeqScan).
func TestIsSimpleSort_SeqScan(t *testing.T) {
	scan := &OP.SeqScan{}
	sort := &OP.Sort{}
	sort.SetChild(scan)
	if !isSimpleSort(sort) {
		t.Error("expected true for Sort(SeqScan)")
	}
}

// REQ001461: isSimpleSort returns true for Sort(Filter(SeqScan)).
func TestIsSimpleSort_FilterSeqScan(t *testing.T) {
	scan := &OP.SeqScan{}
	filter := &OP.Filter{}
	filter.SetChild(scan)
	sort := &OP.Sort{}
	sort.SetChild(filter)
	if !isSimpleSort(sort) {
		t.Error("expected true for Sort(Filter(SeqScan))")
	}
}
