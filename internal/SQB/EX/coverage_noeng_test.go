package EX

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	"github.com/cyw0ng95/razordata/internal/SQB/UT"
)

func TestCoverage_Analyze_WithParams(t *testing.T) {
	a := UT.NewAnalyze(nil)
	a2 := a.WithParams([]any{42})
	if a2 == nil {
		t.Fatal("WithParams returned nil")
	}
}

func TestCoverage_Vacuum_WithParams(t *testing.T) {
	v := UT.NewVacuum(nil)
	v2 := v.WithParams([]any{42})
	if v2 == nil {
		t.Fatal("WithParams returned nil")
	}
}

func TestCoverage_NewUniqueForCatalog(t *testing.T) {
	unique := []DT.UniqueKey{
		{Cols: []int{0, 1}},
	}
	result := WT.NewUniqueForCatalog(unique, []string{"a", "b", "c"})
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
}

func TestCoverage_newUniqueForCatalogEmpty(t *testing.T) {
	result := WT.NewUniqueForCatalog(nil, nil)
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}