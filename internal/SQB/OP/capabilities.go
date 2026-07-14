// REQ001435: adapters that bridge SQB/OP/AG operator method
// names to the pl.* capability interfaces. Many of the methods
// already exist on the operators; the adapters are the small
// naming differences (LeftChild → Child, Idx → IndexName, etc.)
// plus a few capability-marker methods (GroupCols returning
// []string vs []PS.Expr).
//
// The adapters are added once and then never change. The
// concrete operators in SQB/OP/AG remain the source of truth
// for the data; the adapters expose a pl-friendly view.
//
// No constructor is added. The SQB/OP/factory.go wraps the
// existing New* constructors.
package OP

import (
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// --- SeqScan adapters --------------------------------------------------

// Child returns nil. SeqScan is a leaf operator with no child.
func (s *SeqScan) Child() pl.Operator { return nil }

// SetChild is a no-op for SeqScan (it has no child).
func (s *SeqScan) SetChild(_ pl.Operator) {}

// SetUsedCols wraps SeqScan.UsedCols. SeqScan already has UsedCols()
// but the setter is WithUsedCols. The pl.ColPrunable interface
// requires SetUsedCols.
func (s *SeqScan) SetUsedCols(cols []string) { s.usedCols = cols }

// SetRequestedCols is the pl.ColPrunable setter. SeqScan doesn't
// have a separate "requested" concept — UsedCols serves both
// purposes. Forward to UsedCols.
func (s *SeqScan) SetRequestedCols(cols []string) { s.usedCols = cols }

// GetRequestedColsNames is the pl.ColPrunable getter. Aliased
// to UsedCols. Note: SeqScan has a separate `RequestedCols []int`
// field (the int form is the production read path); the pl
// interface uses []string for column names, so we expose
// UsedCols as the string-form projection.
func (s *SeqScan) GetRequestedColsNames() []string { return s.usedCols }

// Columns returns SeqScan's column names, derived from the
// schema. Used by pl.ColumnSchema.
func (s *SeqScan) Columns() []string {
	if s.schema == nil {
		return nil
	}
	out := make([]string, len(s.schema.Cols))
	copy(out, s.schema.Cols)
	return out
}

// ColumnIndex implements pl.ColumnSchema. O(n) lookup; the
// row-level hot path uses ColIndex, not this.
func (s *SeqScan) ColumnIndex(name string) int {
	if s.schema == nil {
		return -1
	}
	for i, c := range s.schema.Cols {
		if c == name {
			return i
		}
	}
	return -1
}

// --- IndexScan adapters ------------------------------------------------

// IndexName returns the index name (pl.IndexInfo). Aliased to
// IndexScan's existing Idx().
func (i *IndexScan) IndexName() string { return i.idx }

// IndexColumns returns the index key columns. IndexScan stores
// range bounds but not the column list; the optimizer pulls
// the list from the catalog (pl.CatalogReader) at pass time.
// Here we return nil to signal "not embedded"; callers should
// use the catalog.
func (i *IndexScan) IndexColumns() []string { return nil }

// IndexTableID returns the LSM table ID. IndexScan doesn't store
// a table ID directly (the table name is sufficient for the
// in-memory path); return 0.
func (i *IndexScan) IndexTableID() uint64 { return 0 }

// SetUsedCols forwards to SeqScan's setter. The pl.ColPrunable
// contract applies to any scan.
func (i *IndexScan) SetUsedCols(cols []string) {
	// IndexScan does not have a usedCols field; the residual
	// filter and index columns are the effective read set.
	// The optimizer tracks "wanted cols" externally.
	_ = cols
}

// SetRequestedCols is a no-op for IndexScan (the index keys
// determine the read set).
func (i *IndexScan) SetRequestedCols(_ []string) {}

// RequestedCols returns nil for IndexScan; the index keys
// are the implicit read set.
func (i *IndexScan) RequestedCols() []string { return nil }

// Columns / ColumnIndex mirror SeqScan.
func (i *IndexScan) Columns() []string {
	if i.schema == nil {
		return nil
	}
	out := make([]string, len(i.schema.Cols))
	copy(out, i.schema.Cols)
	return out
}

func (i *IndexScan) ColumnIndex(name string) int {
	if i.schema == nil {
		return -1
	}
	for idx, c := range i.schema.Cols {
		if c == name {
			return idx
		}
	}
	return -1
}

// --- HashJoin adapters -------------------------------------------------

// Child returns the left child. pl.Parent expects a single child;
// joins return the left.
func (j *HashJoin) Child() pl.Operator { return j.left }

// SetChild sets the left child.
func (j *HashJoin) SetChild(op pl.Operator) {
	if op == nil {
		j.left = nil
		return
	}
	if o, ok := op.(Operator); ok {
		j.left = o
	}
}

// Left returns the left child (pl.Children2).
func (j *HashJoin) Left() pl.Operator { return j.left }

// SetLeft sets the left child (pl.Children2).
func (j *HashJoin) SetLeft(op pl.Operator) {
	j.SetChild(op)
}

// Right returns the right child (pl.Children2).
func (j *HashJoin) Right() pl.Operator { return j.right }

// SetRight sets the right child (pl.Children2).
func (j *HashJoin) SetRight(op pl.Operator) {
	if op == nil {
		j.right = nil
		return
	}
	if o, ok := op.(Operator); ok {
		j.right = o
	}
}

// --- NestedLoopJoin adapters -------------------------------------------

// Child returns the left child.
func (j *NestedLoopJoin) Child() pl.Operator { return j.left }

// SetChild sets the left child. SetLeft/SetRight are already
// defined on NestedLoopJoin in join.go:281-285; we do not
// redefine them here.
func (j *NestedLoopJoin) SetChild(op pl.Operator) {
	if op == nil {
		j.left = nil
		return
	}
	if o, ok := op.(Operator); ok {
		j.left = o
	}
}

func (j *NestedLoopJoin) Left() pl.Operator { return j.left }
func (j *NestedLoopJoin) Right() pl.Operator { return j.right }

// --- Project adapters --------------------------------------------------

// Project already has SetChild (intermediate_basic.go:770).
// No adapter needed.

// --- Sort adapters -----------------------------------------------------

// Sort already has SetChild (intermediate_sort.go:36).
// No adapter needed.

// OrderBy returns the sort keys as pl.OrderSpec. Sort stores
// []PS.OrderItem; we project to []pl.OrderSpec.
func (s *Sort) OrderBy() []pl.OrderSpec {
	if len(s.keys) == 0 {
		return nil
	}
	out := make([]pl.OrderSpec, 0, len(s.keys))
	for _, k := range s.keys {
		col := ""
		if qn, ok := k.Expr.(*PS.QualifiedName); ok {
			col = qn.Name
		}
		out = append(out, pl.OrderSpec{Col: col, Desc: k.Desc})
	}
	return out
}

// --- Limit adapters ----------------------------------------------------

// Limit returns the row cap. LimitValue is the existing method;
// Limit is the pl.LimitInfo name.
func (l *Limit) Limit() int64 { return l.limit }

// Offset returns 0. Limit doesn't store an offset; Offset is a
// separate operator. Returns 0 to signal "no offset on this node".
func (l *Limit) Offset() int64 { return 0 }

// IsTopN returns false. TopN detection (REQ001447) sets this
// on the Sort operator, not Limit.
func (l *Limit) IsTopN() bool { return false }

// SetTopN is a no-op on Limit. TopN is set on Sort.
func (l *Limit) SetTopN(_ bool) {}

// --- CompoundOp adapters -----------------------------------------------

// Child returns the left child. pl.Parent expects one child.
func (c *CompoundOp) Child() pl.Operator { return c.left }

// SetChild sets the left child.
func (c *CompoundOp) SetChild(op pl.Operator) {
	if op == nil {
		c.left = nil
		return
	}
	if o, ok := op.(Operator); ok {
		c.left = o
	}
}

// Left returns the left child (pl.Children2).
func (c *CompoundOp) Left() pl.Operator { return c.left }

// SetLeft sets the left child.
func (c *CompoundOp) SetLeft(op pl.Operator) { c.SetChild(op) }

// Right returns the right child.
func (c *CompoundOp) Right() pl.Operator { return c.right }

// SetRight sets the right child.
func (c *CompoundOp) SetRight(op pl.Operator) {
	if op == nil {
		c.right = nil
		return
	}
	if o, ok := op.(Operator); ok {
		c.right = o
	}
}

// --- Distinct adapters -------------------------------------------------

// SetChild is added for symmetry with the existing Child().
func (d *Distinct) SetChild(op pl.Operator) {
	if op == nil {
		d.child = nil
		return
	}
	if o, ok := op.(Operator); ok {
		d.child = o
	}
}

// --- Filter / FilterProject: SetPredicate ------------------------------

// Filter already has Predicate(); SetPredicate is the setter.
func (f *Filter) SetPredicate(p interface{}) {
	if p == nil {
		f.predicate = nil
		return
	}
	if pe, ok := p.(PS.Expr); ok {
		f.predicate = pe
	}
}

// HasPredicate returns true iff a predicate is set.
func (f *Filter) HasPredicate() bool { return f.predicate != nil }

// FilterProject: SetPredicate + HasPredicate.
func (fp *FilterProject) SetPredicate(p interface{}) {
	if p == nil {
		fp.predicate = nil
		return
	}
	if pe, ok := p.(PS.Expr); ok {
		fp.predicate = pe
	}
}

func (fp *FilterProject) HasPredicate() bool { return fp.predicate != nil }
