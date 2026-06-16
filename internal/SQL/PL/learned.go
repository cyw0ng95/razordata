package PL

import (
	"math"
	"sync"
)

const pairCorrelationBuckets = 100

type pairKey struct {
	table string
	c1, c2 string
}

type LearnedModel struct {
	mu            sync.RWMutex
	trained       bool
	trainingCount int

	correlations map[pairKey]float64
}

var globalLearned = &LearnedModel{
	correlations: make(map[pairKey]float64),
}

func Learned() *LearnedModel { return globalLearned }

func (m *LearnedModel) IsTrained() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.trained
}

func (m *LearnedModel) TrainingCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.trainingCount
}

func (m *LearnedModel) PredicateFeatures(
	predicateType int,
	histogramSelectivity, rowCount, distinctCount, nullCount float64,
) [6]float64 {
	logRow := math.Log(rowCount + 1)
	if logRow > 20 {
		logRow = 20
	}
	distinctFrac := 1.0
	if rowCount > 0 {
		distinctFrac = distinctCount / rowCount
	}
	nullFrac := 0.0
	if rowCount > 0 {
		nullFrac = nullCount / rowCount
	}
	return [6]float64{
		float64(predicateType) / 4.0,
		histogramSelectivity,
		logRow / 20.0,
		distinctFrac,
		nullFrac,
		0.5,
	}
}

func (m *LearnedModel) Predict(features [6]float64) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.trained {
		return features[1]
	}
	return features[1]
}

func (m *LearnedModel) GetCorrelation(table, c1, c2 string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.correlations[pairKey{table: table, c1: c1, c2: c2}]
}

func (m *LearnedModel) SetCorrelation(table, c1, c2 string, val float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if val < -1 {
		val = -1
	}
	if val > 1 {
		val = 1
	}
	m.correlations[pairKey{table: table, c1: c1, c2: c2}] = val
	m.correlations[pairKey{table: table, c1: c2, c2: c1}] = val
	m.trained = true
}

func (m *LearnedModel) AdjustMultiPredicate(baseSelectivity float64, correlations []float64) float64 {
	if len(correlations) == 0 {
		return baseSelectivity
	}
	avgCorr := 0.0
	for _, c := range correlations {
		avgCorr += math.Abs(c)
	}
	avgCorr /= float64(len(correlations))

	factor := 1.0 + avgCorr
	adjusted := baseSelectivity * factor
	if adjusted > 1.0 {
		adjusted = 1.0
	}
	return adjusted
}

func (m *LearnedModel) BootstrapFromHistograms(
	buckets []struct {
		Count         int64
		Lower, Upper  []byte
		TotalRows     int64
		DistinctCount int64
		NullCount     int64
	},
) {
	if len(buckets) < 4 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.trained = true
	m.trainingCount += len(buckets)
}

func (m *LearnedModel) MultiPredicateFeatures(
	predicates []struct {
		Table, Col string
		HistSel    float64
		RowCount   float64
		DistCount  float64
		NullCount  float64
	},
) [6]float64 {
	baseSel := 1.0
	for _, p := range predicates {
		baseSel *= p.HistSel
	}
	if baseSel < 0 {
		baseSel = 0
	}

	totalRows := 0.0
	if len(predicates) > 0 {
		totalRows = predicates[0].RowCount
	}

	return [6]float64{
		float64(len(predicates)) / 10.0,
		baseSel,
		math.Log(totalRows+1) / 20.0,
		0.5,
		0,
		0.5,
	}
}
