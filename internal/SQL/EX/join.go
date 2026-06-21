// Package EX's join.go hosts NestedLoopJoin. Joins are not part of the
// v1 MVP scope per docs/design/ARCH.md; the operator is retained in v1.1 for
// upcoming releases.
package EX

import (
	"context"
	"errors"
	"strings"
)

// JoinKind specifies the type of join.
type JoinKind string

const (
	JoinKindInner JoinKind = "INNER"
	JoinKindLeft  JoinKind = "LEFT"
	JoinKindRight JoinKind = "RIGHT"
	JoinKindFull  JoinKind = "FULL"
	JoinKindCross JoinKind = "CROSS"
)

type NestedLoopJoin struct {
	left      Operator
	right     Operator
	leftTbl   string
	rightTbl  string
	on        func(outer, inner *Row) (bool, error)
	kind      JoinKind
	leftOuter bool // REQ000685: true for LEFT/LEFT OUTER JOIN
	leftRow   *Row
	rightPos  int
	rightRows []Row
	matched   bool // for OUTER JOIN: track if left row found a match
	// REQ000800: hash-based cross join for small tables.
	// When both sides fit in memory, materialize both and
	// do a hash-based cross product instead of NLJ.
	leftRows    []Row
	hashMode    bool
	hashBuckets map[uint64][]int // hash → indices into rightRows
	leftIdx     int
	rightIdx    int
	leftMatched []bool // for LEFT JOIN
}

func NewNestedLoopJoin(left, right Operator, leftTable, rightTable string, on func(outer, inner *Row) (bool, error), kind JoinKind) *NestedLoopJoin {
	return &NestedLoopJoin{
		left:      left,
		right:     right,
		leftTbl:   leftTable,
		rightTbl:  rightTable,
		on:        on,
		kind:      kind,
		leftOuter: kind == JoinKindLeft || kind == JoinKindFull,
	}
}

func (j *NestedLoopJoin) LeftChild() Operator { return j.left }

func (j *NestedLoopJoin) RightChild() Operator { return j.right }

func (j *NestedLoopJoin) Next(ctx context.Context) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	// REQ000800: try hash cross join on first call for small tables.
	if !j.hashMode && j.leftRows == nil && j.rightRows == nil {
		if j.tryHashCrossJoin(ctx) {
			// Switched to hash mode — continue with hash iteration.
		}
	}
	if j.hashMode {
		return j.nextHash(ctx)
	}
	for {
		if j.leftRow == nil {
			row, err := j.left.Next(ctx)
			if err != nil {
				return Row{}, err
			}
			// REQ000725: if the row's columns are already
			// prefixed (e.g. it came from a previous join
			// in a multi-table chain), don't re-prefix — that
			// produces "t29.t51.a51" etc. The NLJ only
			// prefixes the right side; the left side keeps
			// whatever prefix it already has.
			prefixed := Row{Types: row.Types, Data: row.Data, Outer: row.Outer}
			if !hasAnyPrefix(row.Cols) {
				prefixed.Cols = prefixCols(row.Cols, j.leftTbl)
			} else {
				prefixed.Cols = append([]string(nil), row.Cols...)
			}
			j.leftRow = &prefixed
			// REQ000368: drive the right side through its own
			// operator rather than the in-memory `tables` map.
			// The in-memory map is empty for store-backed
			// tables, which caused CROSS JOIN (and any JOIN
			// of store-backed tables) to return zero rows.
			j.rightPos = -1
			j.matched = false
		}
		// Advance the right side. Each call to j.right.Next()
		// yields the next row; we re-init when we've exhausted
		// the right side and need to move to the next left row.
		inner, err := j.advanceRight(ctx)
		if err != nil {
			if err == errRightExhausted {
				// No more right rows for this left row
				if j.leftOuter {
					if !j.matched {
						nullRow := j.nullRightRow()
						result := joinRowsLL(j.leftRow, &nullRow)
						j.leftRow = nil
						return result, nil
					}
				}
				j.leftRow = nil
				continue
			}
			return Row{}, err
		}
		inner.Cols = prefixCols(inner.Cols, j.rightTbl)
		inner.Outer = j.leftRow
		if j.on != nil {
			ok, err := j.on(j.leftRow, &inner)
			if err != nil {
				return Row{}, err
			}
			if !ok {
				continue
			}
		}
		j.matched = true
		return joinRowsLL(j.leftRow, &inner), nil
	}
}

// tryHashCrossJoin attempts to switch to hash mode. Returns true
// if hash mode was activated. REQ000800.
func (j *NestedLoopJoin) tryHashCrossJoin(ctx context.Context) bool {
	// Only for INNER/CROSS without ON clause (pure cross product).
	if j.on != nil || j.leftOuter {
		return false
	}
	// Materialize left side.
	const maxMaterialize = 1024
	j.leftRows = make([]Row, 0, 64)
	for {
		row, err := j.left.Next(ctx)
		if err != nil {
			break
		}
		prefixed := Row{Types: row.Types, Data: row.Data, Outer: row.Outer}
		if !hasAnyPrefix(row.Cols) {
			prefixed.Cols = prefixCols(row.Cols, j.leftTbl)
		} else {
			prefixed.Cols = append([]string(nil), row.Cols...)
		}
		j.leftRows = append(j.leftRows, prefixed)
		if len(j.leftRows) >= maxMaterialize {
			// Too many rows — abort, use NLJ.
			j.leftRows = nil
			return false
		}
	}
	// Materialize right side.
	j.rightRows = make([]Row, 0, 64)
	for {
		row, err := j.right.Next(ctx)
		if err != nil {
			break
		}
		j.rightRows = append(j.rightRows, Row{
			Cols:      prefixCols(row.Cols, j.rightTbl),
			Types:     row.Types,
			Data:      append([]any(nil), row.Data...),
			tableName: row.tableName,
		})
		if len(j.rightRows) >= maxMaterialize {
			// Too many rows — abort, use NLJ.
			j.rightRows = nil
			j.leftRows = nil
			return false
		}
	}
	if len(j.leftRows) == 0 || len(j.rightRows) == 0 {
		// Empty side — result is empty.
		j.leftRows = nil
		j.rightRows = nil
		return false
	}
	// Build hash from right side for equi-join lookups.
	// For cross join, we don't hash — just iterate.
	// Hash is useful for equi-join ON conditions.
	j.hashMode = true
	j.leftIdx = 0
	j.rightIdx = 0
	return true
}

// nextHash produces the next row from the materialized hash join.
// REQ000800.
func (j *NestedLoopJoin) nextHash(_ context.Context) (Row, error) {
	for j.leftIdx < len(j.leftRows) {
		for j.rightIdx < len(j.rightRows) {
			l := j.leftRows[j.leftIdx]
			r := j.rightRows[j.rightIdx]
			j.rightIdx++
			return joinRowsLL(&l, &r), nil
		}
		j.rightIdx = 0
		j.leftIdx++
	}
	// Exhausted.
	j.leftRows = nil
	j.rightRows = nil
	return Row{}, ErrNoRows
}

// errRightExhausted is the sentinel returned by advanceRight when
// the right-side operator has no more rows for the current left row.
var errRightExhausted = errors.New("ex: right side exhausted")

// advanceRight returns the next row from the right-side operator.
// When the right side is exhausted (ErrNoRows), it returns
// errRightExhausted so the outer loop can move to the next left
// row. The right side is reset (Close + re-Next) on each new left
// row so the right's iterator state is rewound.
func (j *NestedLoopJoin) advanceRight(ctx context.Context) (Row, error) {
	if j.rightPos == -1 {
		// First call for this left row: close + reopen the
		// right side so its iterator state is fresh.
		_ = j.right.Close()
		j.rightPos = 0
	}
	row, err := j.right.Next(ctx)
	if err != nil {
		if err == ErrNoRows {
			// Mark so the next call resets the iterator.
			j.rightPos = -1
			return Row{}, errRightExhausted
		}
		return Row{}, err
	}
	return row, nil
}

// nullRightRow returns a row with all NULL values for the right table schema.
func (j *NestedLoopJoin) nullRightRow() Row {
	tablesMu.RLock()
	rightSchema := tables[j.rightTbl]
	tablesMu.RUnlock()

	nullRow := Row{
		Cols:  prefixCols(schemaCols(rightSchema), j.rightTbl),
		Types: schemaTypes(rightSchema),
		Data:  make([]any, len(rightSchema)),
	}
	return nullRow
}

func (j *NestedLoopJoin) Close() error {
	j.hashMode = false
	j.leftRow = nil
	j.leftRows = nil
	j.rightRows = nil
	j.hashBuckets = nil
	j.rightPos = 0
	j.leftIdx = 0
	j.rightIdx = 0
	_ = j.left.Close()
	return j.right.Close()
}

func joinRowsLL(a, b *Row) Row {
	out := Row{}
	out.Cols = append(out.Cols, a.Cols...)
	out.Cols = append(out.Cols, b.Cols...)
	out.Types = append(out.Types, a.Types...)
	out.Types = append(out.Types, b.Types...)
	out.Data = append(out.Data, a.Data...)
	out.Data = append(out.Data, b.Data...)
	return out
}

func prefixCols(cols []string, prefix string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = prefix + "." + c
	}
	return out
}

// hasAnyPrefix reports whether any column name in cols contains
// a dot, indicating it has already been prefixed by a prior join
// in a multi-table chain. REQ000725.
func hasAnyPrefix(cols []string) bool {
	for _, c := range cols {
		if i := strings.IndexByte(c, '.'); i >= 0 && i < len(c)-1 {
			return true
		}
	}
	return false
}

// schemaCols extracts column names from a schema.
func schemaCols(rows []Row) []string {
	if len(rows) == 0 {
		return nil
	}
	return append([]string(nil), rows[0].Cols...)
}

// schemaTypes extracts column types from a schema.
func schemaTypes(rows []Row) []int {
	if len(rows) == 0 {
		return nil
	}
	return append([]int(nil), rows[0].Types...)
}
