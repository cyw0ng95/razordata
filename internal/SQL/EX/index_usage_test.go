package EX

import (
	"testing"
)

func TestIndexUsage_Basic(t *testing.T) {
	iu := NewIndexUsage()
	
	// Register indexes
	iu.RegisterIndex("users", "idx_users_email")
	iu.RegisterIndex("users", "idx_users_name")
	
	// Record index use
	iu.RecordIndexUse("idx_users_email", "users")
	iu.RecordIndexUse("idx_users_email", "users")
	iu.RecordIndexUse("idx_users_name", "users")
	
	// Record index skip
	iu.RecordIndexSkip("idx_users_name", "users", "filter on non-indexed column")
	
	// Get summary
summary := iu.GetUsageSummary()
	t.Logf("Summary: %s", summary)
	
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
}

func TestIndexUsage_Empty(t *testing.T) {
	iu := NewIndexUsage()
summary := iu.GetUsageSummary()
	if summary != "" {
		t.Fatalf("expected empty summary for unused tracker, got %q", summary)
	}
}

func TestMissingIndexSuggestion_Format(t *testing.T) {
	tests := []struct {
		name     string
		s        *MissingIndexSuggestion
		expected string
	}{
		{
			name: "basic single column",
			s: &MissingIndexSuggestion{
				Table:   "users",
				Columns: []string{"email"},
				Reason:  "filter on email used seq scan",
			},
			expected: "IndexHint: consider adding index on users(email) — filter on email used seq scan",
		},
		{
			name: "composite index",
			s: &MissingIndexSuggestion{
				Table:   "orders",
				Columns: []string{"customer_id", "created_at"},
				Reason:  "range query on created_at filtered by customer_id",
			},
			expected: "IndexHint: consider adding index on orders(customer_id, created_at) — range query on created_at filtered by customer_id",
		},
		{
			name: "with estimated benefit",
			s: &MissingIndexSuggestion{
				Table:            "products",
				Columns:          []string{"sku"},
				Reason:           "point lookup by sku",
				EstimatedBenefit: "100x faster",
			},
			expected: "IndexHint: consider adding index on products(sku) — point lookup by sku (100x faster)",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.s.Format()
			if got != tt.expected {
				t.Errorf("Format() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestIndexUsage_Concurrency(t *testing.T) {
	iu := NewIndexUsage()
	
	// Concurrently record index uses
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			iu.RecordIndexUse("idx_a", "t")
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 100; i++ {
			iu.RecordIndexSkip("idx_b", "t", "reason")
		}
		done <- struct{}{}
	}()
	
	<-done
	<-done
	
summary := iu.GetUsageSummary()
	if summary == "" {
		t.Fatal("expected non-empty summary after concurrent updates")
	}
}
