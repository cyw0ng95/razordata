package MM

import (
	"math"
	"testing"
)

func TestLearnedModel_IsTrained(t *testing.T) {
	m := &LearnedModel{correlations: make(map[pairKey]float64)}
	if m.IsTrained() {
		t.Error("new model should not be trained")
	}
	m.BootstrapFromHistograms([]struct {
		Count         int64
		Lower, Upper  []byte
		TotalRows     int64
		DistinctCount int64
		NullCount     int64
	}{
		{Count: 100, Lower: []byte("a"), Upper: []byte("z"), TotalRows: 1000, DistinctCount: 50, NullCount: 10},
		{Count: 200, Lower: []byte("a"), Upper: []byte("z"), TotalRows: 1000, DistinctCount: 50, NullCount: 10},
		{Count: 50, Lower: []byte("a"), Upper: []byte("z"), TotalRows: 1000, DistinctCount: 50, NullCount: 10},
		{Count: 150, Lower: []byte("a"), Upper: []byte("z"), TotalRows: 1000, DistinctCount: 50, NullCount: 10},
	})
	if !m.IsTrained() {
		t.Error("model should be trained after bootstrap with 4+ buckets")
	}
}

func TestLearnedModel_BootstrapTooFew(t *testing.T) {
	m := &LearnedModel{correlations: make(map[pairKey]float64)}
	m.BootstrapFromHistograms([]struct {
		Count         int64
		Lower, Upper  []byte
		TotalRows     int64
		DistinctCount int64
		NullCount     int64
	}{
		{Count: 100, Lower: []byte("a"), Upper: []byte("z"), TotalRows: 1000, DistinctCount: 50, NullCount: 10},
	})
	if m.IsTrained() {
		t.Error("model should not train with fewer than 4 buckets")
	}
}

func TestLearnedModel_Correlation(t *testing.T) {
	m := &LearnedModel{correlations: make(map[pairKey]float64)}

	m.SetCorrelation("t1", "a", "b", 0.7)
	if got := m.GetCorrelation("t1", "a", "b"); math.Abs(got-0.7) > 0.001 {
		t.Errorf("correlation a,b = %.4f, want 0.7", got)
	}
	if got := m.GetCorrelation("t1", "b", "a"); math.Abs(got-0.7) > 0.001 {
		t.Errorf("symmetric correlation b,a = %.4f, want 0.7", got)
	}
	if got := m.GetCorrelation("t2", "a", "b"); got != 0 {
		t.Errorf("different table should return 0, got %.4f", got)
	}
}

func TestLearnedModel_CorrelationClamp(t *testing.T) {
	m := &LearnedModel{correlations: make(map[pairKey]float64)}
	m.SetCorrelation("t", "x", "y", 2.5)
	if got := m.GetCorrelation("t", "x", "y"); got != 1.0 {
		t.Errorf("over-range should clamp to 1.0, got %.4f", got)
	}
	m.SetCorrelation("t", "x", "y", -2.5)
	if got := m.GetCorrelation("t", "x", "y"); got != -1.0 {
		t.Errorf("under-range should clamp to -1.0, got %.4f", got)
	}
}

func TestLearnedModel_AdjustMultiPredicate(t *testing.T) {
	m := &LearnedModel{correlations: make(map[pairKey]float64)}

	sel := m.AdjustMultiPredicate(0.1, nil)
	if math.Abs(sel-0.1) > 0.001 {
		t.Errorf("no correlations: %.4f, want 0.1", sel)
	}

	sel = m.AdjustMultiPredicate(0.5, []float64{0.5, 0.3})
	expected := 0.5 * (1 + 0.4)
	if math.Abs(sel-expected) > 0.001 {
		t.Errorf("with correlations: %.4f, want %.4f", sel, expected)
	}

	sel = m.AdjustMultiPredicate(0.8, []float64{0.9})
	if sel != 1.0 {
		t.Errorf("overflow should clamp to 1.0, got %.4f", sel)
	}
}

func TestLearnedModel_Predict_default(t *testing.T) {
	m := &LearnedModel{correlations: make(map[pairKey]float64)}
	feat := [6]float64{0, 0.3, 0.5, 0.5, 0, 0.5}
	pred := m.Predict(feat)
	if math.Abs(pred-0.3) > 0.001 {
		t.Errorf("untrained predict should return features[1], got %.4f", pred)
	}
}

func TestLearnedModel_PredicateFeatures(t *testing.T) {
	m := &LearnedModel{correlations: make(map[pairKey]float64)}
	feat := m.PredicateFeatures(0, 0.1, 1000, 50, 10)
	if feat[1] != 0.1 {
		t.Errorf("histogram selectivity feature should be 0.1, got %.4f", feat[1])
	}
	if feat[3] != 0.05 {
		t.Errorf("distinct fraction should be 0.05, got %.4f", feat[3])
	}
}

func TestLearnedModel_PredicateFeaturesZeroRowCount(t *testing.T) {
	m := &LearnedModel{correlations: make(map[pairKey]float64)}
	feat := m.PredicateFeatures(0, 0.1, 0, 0, 0)
	if feat[1] != 0.1 {
		t.Errorf("histogram selectivity should be 0.1, got %.4f", feat[1])
	}
}

func TestLearnedModel_PredicateFeaturesClampLog(t *testing.T) {
	m := &LearnedModel{correlations: make(map[pairKey]float64)}
	feat := m.PredicateFeatures(0, 0.1, 1e12, 1e6, 0)
	if feat[2] > 1.0 {
		t.Errorf("log row normalized should be <= 1.0, got %.4f", feat[2])
	}
}

func TestLearnedModel_MultiPredicateFeatures(t *testing.T) {
	m := &LearnedModel{correlations: make(map[pairKey]float64)}
	preds := []struct {
		Table, Col string
		HistSel    float64
		RowCount   float64
		DistCount  float64
		NullCount  float64
	}{
		{Table: "t", Col: "a", HistSel: 0.1, RowCount: 1000, DistCount: 100, NullCount: 10},
		{Table: "t", Col: "b", HistSel: 0.2, RowCount: 1000, DistCount: 50, NullCount: 5},
	}
	feat := m.MultiPredicateFeatures(preds)
	expectedBase := 0.1 * 0.2
	if math.Abs(feat[1]-expectedBase) > 0.001 {
		t.Errorf("base selectivity = %.4f, want %.4f", feat[1], expectedBase)
	}
	if math.Abs(feat[0]-0.2) > 0.001 {
		t.Errorf("predicate count normalized = %.4f, want 0.2", feat[0])
	}
}

func TestLearnedModel_TrainingCount(t *testing.T) {
	m := &LearnedModel{correlations: make(map[pairKey]float64)}
	m.BootstrapFromHistograms([]struct {
		Count         int64
		Lower, Upper  []byte
		TotalRows     int64
		DistinctCount int64
		NullCount     int64
	}{
		{Count: 100, Lower: []byte("a"), Upper: []byte("z"), TotalRows: 1000, DistinctCount: 50, NullCount: 10},
		{Count: 200, Lower: []byte("a"), Upper: []byte("z"), TotalRows: 1000, DistinctCount: 50, NullCount: 10},
		{Count: 50, Lower: []byte("a"), Upper: []byte("z"), TotalRows: 1000, DistinctCount: 50, NullCount: 10},
		{Count: 150, Lower: []byte("a"), Upper: []byte("z"), TotalRows: 1000, DistinctCount: 50, NullCount: 10},
		{Count: 300, Lower: []byte("a"), Upper: []byte("z"), TotalRows: 1000, DistinctCount: 50, NullCount: 10},
	})
	if m.TrainingCount() != 5 {
		t.Errorf("training count should be 5, got %d", m.TrainingCount())
	}
}

func TestLearnedModel_ConcurrentRead(t *testing.T) {
	m := &LearnedModel{correlations: make(map[pairKey]float64)}
	m.BootstrapFromHistograms([]struct {
		Count         int64
		Lower, Upper  []byte
		TotalRows     int64
		DistinctCount int64
		NullCount     int64
	}{
		{Count: 100, Lower: []byte("a"), Upper: []byte("z"), TotalRows: 1000, DistinctCount: 50, NullCount: 10},
		{Count: 200, Lower: []byte("a"), Upper: []byte("z"), TotalRows: 1000, DistinctCount: 50, NullCount: 10},
		{Count: 50, Lower: []byte("a"), Upper: []byte("z"), TotalRows: 1000, DistinctCount: 50, NullCount: 10},
		{Count: 150, Lower: []byte("a"), Upper: []byte("z"), TotalRows: 1000, DistinctCount: 50, NullCount: 10},
	})
	done := make(chan struct{}, 10)
	for i := 0; i < 10; i++ {
		go func() {
			m.IsTrained()
			m.TrainingCount()
			m.Predict([6]float64{0, 0.1, 0.5, 0.1, 0, 0.5})
			m.GetCorrelation("t", "a", "b")
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestLearnedModel_ConcurrentWrite(t *testing.T) {
	m := &LearnedModel{correlations: make(map[pairKey]float64)}
	done := make(chan struct{}, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			m.SetCorrelation("t", "a", "b", float64(idx)/10.0)
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
