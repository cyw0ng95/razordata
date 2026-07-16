package UT

import (
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
)

import (
	"context"
	"testing"
)

// REQ001448: TelemetryAtomicBatchProducer records correct metrics.
func TestTelemetryAtomicBatchProducer_RecordsMetrics(t *testing.T) {
	collector := NewTelemetryCollector()
	
	// Create a fake producer yielding 2 batches of 3 and 2 rows.
	batches := []*Batch{
		func() *Batch {
			b := GetBatch(1)
			b.SetColumnName(0, "x")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{1, 2, 3} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
		func() *Batch {
			b := GetBatch(1)
			b.SetColumnName(0, "x")
			b.Cols[0].Type = LX.T_INT_KW
			for _, v := range []int64{4, 5} {
				b.AppendRow(0, LX.T_INT_KW, v, false)
				b.AdvanceSize()
			}
			return b
		}(),
	}
	child := &fakeTelemProducer{batches: batches}
	
	tp := NewTelemetryAtomicBatchProducer(child, "TestOp", collector)
	ctx := context.Background()
	
	// Drain all batches.
	for {
		batch, err := tp.NextBatch(ctx)
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		batch.Put()
	}
	
	// Verify collected telemetry.
	records := collector.GetRecords()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	
	rec := records[0]
	if rec.OpName != "TestOp" {
		t.Errorf("OpName: got %q, want %q", rec.OpName, "TestOp")
	}
	if rec.Rows != 5 {
		t.Errorf("Rows: got %d, want 5", rec.Rows)
	}
	if rec.Batches != 2 {
		t.Errorf("Batches: got %d, want 2", rec.Batches)
	}
	if rec.WallNs <= 0 {
		t.Errorf("WallNs: got %d, want > 0", rec.WallNs)
	}
	if !rec.UsedBatchMode {
		t.Error("UsedBatchMode should be true")
	}
}

// REQ001448: TelemetryCollector.Aggregate groups by operator name.
func TestTelemetryCollector_Aggregate(t *testing.T) {
	collector := NewTelemetryCollector()
	
	// Add 3 records with same op name.
	collector.Record(&BatchTelemetry{OpName: "SeqScan", Rows: 100, Batches: 10, WallNs: 1000000})
	collector.Record(&BatchTelemetry{OpName: "SeqScan", Rows: 200, Batches: 20, WallNs: 2000000})
	collector.Record(&BatchTelemetry{OpName: "Filter", Rows: 150, Batches: 15, WallNs: 1500000})
	
	agg := collector.Aggregate()
	if len(agg) != 2 {
		t.Fatalf("expected 2 aggregated records, got %d", len(agg))
	}
	
	// Find SeqScan aggregate.
	var seqScan *AggregatedTelemetry
	for i := range agg {
		if agg[i].OpName == "SeqScan" {
			seqScan = &agg[i]
			break
		}
	}
	if seqScan == nil {
		t.Fatal("SeqScan not found in aggregate")
	}
	if seqScan.TotalRows != 300 {
		t.Errorf("TotalRows: got %d, want 300", seqScan.TotalRows)
	}
	if seqScan.TotalBatches != 30 {
		t.Errorf("TotalBatches: got %d, want 30", seqScan.TotalBatches)
	}
	expectedAvg := float64(300) / float64(30)
	if seqScan.AvgRowsPerBat != expectedAvg {
		t.Errorf("AvgRowsPerBat: got %f, want %f", seqScan.AvgRowsPerBat, expectedAvg)
	}
}

// REQ001448: Context telemetry passthrough.
func TestTelemetryContextPassthrough(t *testing.T) {
	collector := NewTelemetryCollector()
	ctx := WithTelemetryCollector(context.Background(), collector)
	
	got := GetTelemetryCollector(ctx)
	if got != collector {
		t.Error("context should return the same collector")
	}
	
	ctx2 := context.Background()
	got2 := GetTelemetryCollector(ctx2)
	if got2 != nil {
		t.Errorf("empty context should return nil, got %v", got2)
	}
}

// fakeTelemProducer implements BatchProducer for telemetry tests.
type fakeTelemProducer struct {
	batches []*Batch
	idx     int
}

func (f *fakeTelemProducer) NextBatch(ctx context.Context) (*Batch, error) {
	if f.idx >= len(f.batches) {
		return nil, nil
	}
	b := f.batches[f.idx]
	f.idx++
	return b, nil
}

func (f *fakeTelemProducer) Close() error { return nil }
