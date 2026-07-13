package OP

import (
	"context"
	"sync/atomic"

	ec "github.com/cyw0ng95/razordata/internal/LOG/EC"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
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
// When the inner operator is a *IndexScan, NewIndexOnlyScan also
// activates the scan's covering-mode fast path (REQ001249): the
// next-row loop builds the result Row directly from the encoded
// index value + primary key, eliminating the per-row Store.Get
// syscall. Tests that wrap a non-IndexScan operator skip this
// optimisation and continue to exercise the basic wrapper.
type IndexOnlyScan struct {
	inner  pl.Operator
	closed atomic.Bool
}

// NewIndexOnlyScan wraps an operator that emits index rows. When
// the underlying operator is an *IndexScan the scan is switched to
// covering mode via SetCovering, which causes nextFromIndex to
// skip the heap fetch and synthesize the Row from the encoded
// index value plus primary key. idxCols / idxTypes must describe
// the index column order/types so the encoded suffix can be
// decoded back into typed Values. pk is the column whose raw
// bytes are stored as the index entry's value; pass "" if the
// projected Row does not require the primary key (the planner
// only enters this branch when the projection includes PK,
// otherwise covering-index candidates are not selected at all).
// REQ001107 / REQ001249.
func NewIndexOnlyScan(inner pl.Operator, idxCols []string, idxTypes []LX.TokenType, pk string) *IndexOnlyScan {
	if inner == nil {
		return nil
	}
	if isc, ok := inner.(*IndexScan); ok {
		isc.SetCovering(idxCols, idxTypes, pk)
	}
	return &IndexOnlyScan{inner: inner}
}

// NewIndexOnlyScanPassthrough wraps any operator without enabling
// the covering-mode fast path. Useful for tests that wire a stub
// IndexScan with their own Next semantics. REQ001107.
func NewIndexOnlyScanPassthrough(inner pl.Operator) *IndexOnlyScan {
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
