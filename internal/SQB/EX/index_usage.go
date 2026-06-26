package EX

import (
	"fmt"
	"strings"
	"sync"
)

// IndexUsage tracks index usage statistics across query execution.
// REQ000790: Index diagnostics for EXPLAIN output.
type IndexUsage struct {
	mu            sync.Mutex
	IndexUsed     map[string]int64  // idxName -> usage count
	IndexSkipped  map[string]int64  // idxName -> skip count (SeqScan on indexed col)
	TableIndexes  map[string][]string // tableName -> available index names
}

// NewIndexUsage creates a new IndexUsage tracker.
func NewIndexUsage() *IndexUsage {
	return &IndexUsage{
		IndexUsed:    make(map[string]int64),
		IndexSkipped: make(map[string]int64),
		TableIndexes: make(map[string][]string),
	}
}

// RecordIndexUse records that an index was used for a query.
func (iu *IndexUsage) RecordIndexUse(idxName, tableName string) {
	iu.mu.Lock()
	defer iu.mu.Unlock()
	iu.IndexUsed[idxName]++
}

// RecordIndexSkip records that an index was available but not used.
func (iu *IndexUsage) RecordIndexSkip(idxName, tableName string, reason string) {
	iu.mu.Lock()
	defer iu.mu.Unlock()
	iu.IndexSkipped[idxName]++
}

// RegisterIndex registers an index on a table.
func (iu *IndexUsage) RegisterIndex(tableName, idxName string) {
	iu.mu.Lock()
	defer iu.mu.Unlock()
	iu.TableIndexes[tableName] = append(iu.TableIndexes[tableName], idxName)
}

// GetUsageSummary returns a summary of index usage.
func (iu *IndexUsage) GetUsageSummary() string {
	iu.mu.Lock()
	defer iu.mu.Unlock()
	
	var b strings.Builder
	totalUsed := int64(0)
	totalSkipped := int64(0)
	
	for _, cnt := range iu.IndexUsed {
		totalUsed += cnt
	}
	for _, cnt := range iu.IndexSkipped {
		totalSkipped += cnt
	}
	
	if totalUsed == 0 && totalSkipped == 0 {
		return ""
	}
	
	b.WriteString("IndexUsage: ")
	if totalUsed > 0 {
		b.WriteString("used=")
		b.WriteString(fmt.Sprintf("%d", totalUsed))
	}
	if totalSkipped > 0 {
		if totalUsed > 0 {
			b.WriteString(", ")
		}
		b.WriteString("skipped=")
		b.WriteString(fmt.Sprintf("%d", totalSkipped))
	}
	
	return b.String()
}

// MissingIndexSuggestion suggests missing indexes based on query patterns.
// REQ000790: Index hint annotation in EXPLAIN output.
type MissingIndexSuggestion struct {
	Table            string
	Columns          []string
	Reason           string
	EstimatedBenefit string
}

// Format returns a human-readable suggestion.
func (s *MissingIndexSuggestion) Format() string {
	var b strings.Builder
	b.WriteString("IndexHint: ")
	b.WriteString("consider adding index on ")
	b.WriteString(s.Table)
	if len(s.Columns) > 0 {
		b.WriteString("(")
		b.WriteString(strings.Join(s.Columns, ", "))
		b.WriteString(")")
	}
	b.WriteString(" — ")
	b.WriteString(s.Reason)
	if s.EstimatedBenefit != "" {
		b.WriteString(" (")
		b.WriteString(s.EstimatedBenefit)
		b.WriteString(")")
	}
	return b.String()
}
