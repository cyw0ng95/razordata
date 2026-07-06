package OP

import (
	"context"
	"sync/atomic"

	ec "github.com/cyw0ng95/razordata/internal/LOG/EC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// IndexOnlyScan reads directly from a secondary-index keyspace
// without fetching the corresponding heap rows. REQ001107.
//
// The "index-only" optimization applies when the query's SELECT
// columns are all covered by the index (the index value carries
// enough information to satisfy the projection). For razor-data,
// the index entry stores the secondary-key + primary-key; if the
// query projects only indexed columns (and optionally the primary
// key), the planner wires IndexOnlyScan instead of IndexScan to
// skip the heap fetch.
//
// The current implementation forwards Next/Close to any
// pl.Operator. The planner enforces the covering-index invariant;
// tests can plug in a custom index-scan stub to verify behaviour
// without touching the heap.
type IndexOnlyScan struct {
	inner  pl.Operator
	closed atomic.Bool
}

// NewIndexOnlyScan wraps any operator that emits index rows. The
// caller (planner) is responsible for selecting an operator whose
// output already covers the projected columns.
func NewIndexOnlyScan(inner pl.Operator) *IndexOnlyScan {
	if inner == nil {
		return nil
	}
	return &IndexOnlyScan{inner: inner}
}

// Table returns the empty string for the generic-operator flavour.
// Callers that need the table name should keep the underlying
// IndexScan pointer alongside this wrapper.
func (s *IndexOnlyScan) Table() string { return "" }

// Inner exposes the wrapped operator for tests and planner code
// that needs to inspect the underlying scan.
func (s *IndexOnlyScan) Inner() pl.Operator { return s.inner }

// Next forwards to the wrapped operator.
func (s *IndexOnlyScan) Next(ctx context.Context) (pl.Row, error) {
	if s == nil || s.inner == nil {
		return pl.Row{}, pl.ErrNoRows
	}
	ec.BUG_ON(s.closed.Load(), "IndexOnlyScan.Next() after Close()")
	return s.inner.Next(ctx)
}

// Close releases the wrapped operator.
func (s *IndexOnlyScan) Close() error {
	if s == nil || s.inner == nil {
		return nil
	}
	s.closed.Store(true)
	return s.inner.Close()
}

// IsCoveringIndex returns true when the supplied projected columns
// are all covered by the index columns (plus optionally the
// primary key). REQ001107. Empty projection is treated as a
// covering query (no columns needed).
func IsCoveringIndex(projected []string, indexCols []string, pk string) bool {
	if len(projected) == 0 {
		return true
	}
	if len(indexCols) == 0 && pk == "" {
		return false
	}
	covered := make(map[string]bool, len(indexCols)+1)
	for _, c := range indexCols {
		covered[c] = true
	}
	if pk != "" {
		covered[pk] = true
	}
	for _, p := range projected {
		if !covered[p] {
			return false
		}
	}
	return true
}
