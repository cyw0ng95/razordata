package EX

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type Filter struct {
	child     Operator
	predicate PS.Expr
	params    []any
	execCtx   *ExecContext
	// REQ000757: curRow avoids heap-escape of local row variable
	// when passing &row to Eval. Filter is heap-allocated, so
	// &f.curRow is already a heap pointer — no escape needed.
	curRow Row
	// REQ000802: compiledFilterFn is a specialized predicate function
	// compiled on first use. It bypasses Eval dispatch overhead.
	compiledFilterFn func(*Row) (bool, error)
	compiledOnce     bool
}

// Child returns the filter's child operator. Used by
// propagateParams to walk the operator tree.
func (f *Filter) Child() Operator { return f.child }

// Predicate returns the filter's predicate expression.
func (f *Filter) Predicate() PS.Expr { return f.predicate }

func NewFilter(child Operator, predicate PS.Expr) *Filter {
	return &Filter{
		child:     child,
		predicate: predicate,
	}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (f *Filter) WithParams(p []any) Operator {
	f.params = p
	return f
}

func (f *Filter) Next(ctx context.Context) (Row, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		r, err := f.child.Next(ctx)
		if err != nil {
			return Row{}, err
		}
		if f.execCtx != nil {
			r.execCtx = f.execCtx
		}
		if f.predicate == nil {
			return r, nil
		}
		// REQ000802: use compiled predicate if available.
		if !f.compiledOnce {
			f.compiledFilterFn = compileFilterExpr(f.predicate)
			f.compiledOnce = true
		}
		if f.compiledFilterFn != nil {
			ok, cerr := f.compiledFilterFn(&r)
			if cerr != nil {
				return Row{}, cerr
			}
			if ok {
				return r, nil
			}
			continue
		}
		// Fallback to Eval-based path.
		f.curRow = r
		v, err := Eval(f.predicate, &f.curRow, f.params)
		if err != nil {
			return Row{}, err
		}
		if truthy(v) {
			return f.curRow, nil
		}
	}
}

func (f *Filter) Close() error {
	return f.child.Close()
}

type Project struct {
	child     Operator
	cols      []PS.Expr
	params    []any
	// REQ000756: pre-allocated column names (same for every row).
	prefixCols []string
	// REQ000816: pre-built colIndex map shared across all output
	// rows. Avoids per-row buildColIndex in Lookup (pprof: 23.45%
	// cum, 1.06s in j3_mixed).
	colIndex map[string]int
}

// Child returns the project's child operator.
func (p *Project) Child() Operator { return p.child }

func NewProject(child Operator, cols []PS.Expr) *Project {
	// Pre-compute column names once (they're the same for every row).
	prefixCols := make([]string, len(cols))
	for i, c := range cols {
		var name string
		switch e := c.(type) {
		case *PS.Ident:
			name = e.Name
		case *PS.QualifiedName:
			// REQ000720: render qualified name as "table.col"
			// so the projected column matches what callers
			// expect when the query uses a table alias.
			name = e.Table + "." + e.Name
		case *PS.AliasedExpr:
			if inner, ok := e.Expr.(*PS.Ident); ok {
				name = inner.Name
			}
			if name == "" {
				name = e.Alias
			}
		case *PS.UnaryExpr:
			if inner, ok := e.Operand.(*PS.Ident); ok {
				name = inner.Name
			}
		case *PS.WindowFunc:
			name = e.Name
		}
		if a, ok := c.(*PS.AliasedExpr); ok {
			name = a.Alias
		}
		prefixCols[i] = name
	}
	// REQ000816: build colIndex once. All prefixCols are
	// already lowercase (parser lowercases at parse time per
	// REQ000770).
	colIndex := make(map[string]int, len(prefixCols))
	for i, c := range prefixCols {
		colIndex[c] = i
	}
	return &Project{
		child:      child,
		cols:       cols,
		prefixCols: prefixCols,
		colIndex:   colIndex,
	}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (p *Project) WithParams(p2 []any) Operator {
	p.params = p2
	return p
}

func (p *Project) Next(ctx context.Context) (Row, error) {
	row, err := p.child.Next(ctx)
	if err != nil {
		return Row{}, err
	}
	if isStar(p.cols) {
		return row, nil
	}
	// REQ000756: use pre-computed column names, allocate only data.
	out := Row{
		Cols:     append([]string(nil), p.prefixCols...),
		Data:     make([]Value, len(p.cols)),
		colIndex: p.colIndex,
	}
	for i, c := range p.cols {
		var v any
		var err error
		if wf, ok := c.(*PS.WindowFunc); ok {
			v, err = findColumn(row, wf.Name)
			if err != nil {
				return Row{}, err
			}
		} else {
			v, err = Eval(c, &row, p.params)
			if err != nil {
				return Row{}, err
			}
		}
		out.Data[i] = valueFromAny(v)
	}
	return out, nil
}

func isStar(cols []PS.Expr) bool {
	if len(cols) == 1 {
		_, ok := cols[0].(*PS.StarExpr)
		return ok
	}
	return false
}

func findColumn(row Row, name string) (any, error) {
	for i, c := range row.Cols {
		if c == name {
			return row.Data[i], nil
		}
	}
	return nil, fmt.Errorf("ex: column %q not found in row", name)
}

func (p *Project) Close() error {
	return p.child.Close()
}

type Sort struct {
	child        Operator
	keys         []PS.OrderItem
	buf          []Row
	pos          int
	materialized bool
	params       []any
}

// Child returns the sort's child operator.
func (s *Sort) Child() Operator { return s.child }

func NewSort(child Operator, keys []PS.OrderItem) *Sort {
	return &Sort{child: child, keys: keys}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (s *Sort) WithParams(p []any) Operator {
	s.params = p
	return s
}

func (s *Sort) Next(ctx context.Context) (Row, error) {
	if !s.materialized {
		for {
			row, err := s.child.Next(ctx)
			if err != nil {
				if err == ErrNoRows {
					break
				}
				return Row{}, err
			}
			s.buf = append(s.buf, row)
		}

		// REQ000768+REQ000773: pre-extract sort keys into a parallel
		// keyCache slice, then sort an index array in-place.
		// Eliminates the sortRow allocation and double-buffering.
		n := len(s.buf)
		keyCache := make([][]Value, n)
		for i, r := range s.buf {
			sk := make([]Value, len(s.keys))
			for j, k := range s.keys {
				v, err := Eval(k.Expr, &r, s.params)
				if err != nil {
					return Row{}, err
				}
				sk[j] = valueFromAny(v)
			}
			keyCache[i] = sk
		}

		indices := make([]int, n)
		for i := range indices {
			indices[i] = i
		}
		slices.SortStableFunc(indices, func(ai, bi int) int {
			ka, kb := keyCache[ai], keyCache[bi]
			for ki := range ka {
				if s.keys[ki].NullsOrder != 0 {
					if ka[ki].IsNull() && !kb[ki].IsNull() {
						return -int(s.keys[ki].NullsOrder)
					}
					if kb[ki].IsNull() && !ka[ki].IsNull() {
						return int(s.keys[ki].NullsOrder)
					}
				}
				c := compare(ka[ki], kb[ki])
				if c == 0 {
					continue
				}
				if s.keys[ki].Desc {
					return -c
				}
				return c
			}
			return 0
		})

		// Reorder s.buf in-place using sorted indices.
		reordered := make([]Row, n)
		for i, idx := range indices {
			reordered[i] = s.buf[idx]
		}
		s.buf = reordered
		s.materialized = true
	}
	if s.pos >= len(s.buf) {
		return Row{}, ErrNoRows
	}
	r := s.buf[s.pos]
	s.pos++
	return r, nil
}

func (s *Sort) Close() error {
	s.buf = nil
	s.pos = 0
	s.materialized = false
	return s.child.Close()
}

type Limit struct {
	child  Operator
	limit  int64
	seen   int64
	params []any
}

// Child returns the limit's child operator.
func (l *Limit) Child() Operator { return l.child }

// LimitValue returns the limit value.
func (l *Limit) LimitValue() int64 { return l.limit }

func NewLimit(child Operator, n int64) *Limit {
	return &Limit{child: child, limit: n}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (l *Limit) WithParams(p []any) Operator {
	l.params = p
	return l
}

func (l *Limit) Next(ctx context.Context) (Row, error) {
	if l.seen >= l.limit {
		return Row{}, ErrNoRows
	}
	row, err := l.child.Next(ctx)
	if err != nil {
		return Row{}, err
	}
	l.seen++
	return row, nil
}

func (l *Limit) Close() error {
	l.seen = 0
	return l.child.Close()
}

// Offset skips the first n rows from its child before yielding. It pairs
// with Limit to implement LIMIT/OFFSET pagination. A nil child or a
// negative n is treated as zero (no offset).
type Offset struct {
	child   Operator
	offset  int64
	skipped int64
	params  []any
}

// Child returns the offset's child operator.
func (o *Offset) Child() Operator { return o.child }

func NewOffset(child Operator, n int64) *Offset {
	if n < 0 {
		n = 0
	}
	return &Offset{child: child, offset: n}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (o *Offset) WithParams(p []any) Operator {
	o.params = p
	return o
}

func (o *Offset) Next(ctx context.Context) (Row, error) {
	for o.skipped < o.offset {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		if _, err := o.child.Next(ctx); err != nil {
			return Row{}, err
		}
		o.skipped++
	}
	return o.child.Next(ctx)
}

func (o *Offset) Close() error {
	o.skipped = 0
	if o.child == nil {
		return nil
	}
	return o.child.Close()
}

// compileFilterExpr compiles a simple Filter predicate into a
// specialized function that reads directly from row.Data, bypassing
// the Eval dispatch tree. Returns nil for complex predicates that
// cannot be compiled. REQ000802.
func compileFilterExpr(e PS.Expr) func(*Row) (bool, error) {
	if e == nil {
		return nil
	}
	switch v := e.(type) {
	case *PS.BinaryExpr:
		return compileBinary(v)
	case *PS.UnaryExpr:
		if v.Op == int(LX.T_NOT) {
			inner := compileFilterExpr(v.Operand)
			if inner == nil {
				return nil
			}
			return func(row *Row) (bool, error) {
				res, err := inner(row)
				if err != nil {
					return false, err
				}
				return !res, nil
			}
		}
		return nil
	default:
		return nil
	}
}

func compileBinary(e *PS.BinaryExpr) func(*Row) (bool, error) {
	// Handle AND/OR by compiling both sides.
	if e.Op == int(LX.T_AND) {
		left := compileFilterExpr(e.Left)
		right := compileFilterExpr(e.Right)
		if left == nil || right == nil {
			return nil
		}
		return func(row *Row) (bool, error) {
			lr, err := left(row)
			if err != nil || !lr {
				return false, err
			}
			return right(row)
		}
	}
	if e.Op == int(LX.T_OR) {
		left := compileFilterExpr(e.Left)
		right := compileFilterExpr(e.Right)
		if left == nil || right == nil {
			return nil
		}
		return func(row *Row) (bool, error) {
			lr, err := left(row)
			if err != nil || lr {
				return lr, err
			}
			return right(row)
		}
	}

	// Simple comparisons: col OP literal
	col, literal, ok := extractColLiteralPair(e)
	if !ok {
		return nil
	}
	if literal == nil {
		return nil
	}
	colName := col
	litVal := literal

	switch e.Op {
	case int(LX.T_EQ):
		return makeCompiledCmp(colName, litVal, func(a, b any) bool {
			return equalValue(a, b)
		})
	case int(LX.T_NE):
		return makeCompiledCmp(colName, litVal, func(a, b any) bool {
			return !equalValue(a, b)
		})
	case int(LX.T_GT):
		return makeCompiledCmp(colName, litVal, func(a, b any) bool {
			return compare(a, b) > 0
		})
	case int(LX.T_GE):
		return makeCompiledCmp(colName, litVal, func(a, b any) bool {
			return compare(a, b) >= 0
		})
	case int(LX.T_LT):
		return makeCompiledCmp(colName, litVal, func(a, b any) bool {
			return compare(a, b) < 0
		})
	case int(LX.T_LE):
		return makeCompiledCmp(colName, litVal, func(a, b any) bool {
			return compare(a, b) <= 0
		})
	}
	return nil
}

func makeCompiledCmp(colName string, litVal any, cmp func(a, b any) bool) func(*Row) (bool, error) {
	idx := -1
	bareName := colName
	if dot := strings.LastIndexByte(colName, '.'); dot >= 0 {
		bareName = colName[dot+1:]
	}
	return func(row *Row) (bool, error) {
		if idx < 0 {
			// Try direct match first (qualified name like "t1.a").
			for i, c := range row.Cols {
				if strings.EqualFold(c, colName) {
					idx = i
					break
				}
			}
			// Fallback: try bare column name (works for SeqScan rows).
			if idx < 0 {
				for i, c := range row.Cols {
					if strings.EqualFold(c, bareName) && i < len(row.Data) {
						idx = i
						break
					}
				}
			}
			// Fallback: suffix match for bare names on prefixed rows.
			if idx < 0 {
				lk := strings.ToLower(bareName)
				for i, c := range row.Cols {
					if strings.HasSuffix(strings.ToLower(c), "."+lk) && i < len(row.Data) {
						idx = i
						break
					}
				}
			}
			if idx < 0 {
				return false, nil
			}
		}
		if idx >= len(row.Data) {
			return false, nil
		}
		return cmp(row.Data[idx], litVal), nil
	}
}

// extractColLiteralPair extracts (column_name, literal_value, ok) from a
// BinaryExpr where one side is a column reference and the other is a literal.
// REQ000802: supports both Ident (bare name) and QualifiedName (table.col).
func extractColLiteralPair(e *PS.BinaryExpr) (string, any, bool) {
	if col, ok := colRefName(e.Left); ok {
		if lit, ok := extractLiteral(e.Right); ok {
			return col, lit, true
		}
	}
	if col, ok := colRefName(e.Right); ok {
		if lit, ok := extractLiteral(e.Left); ok {
			return col, lit, true
		}
	}
	return "", nil, false
}

// colRefName returns the column name from an expression that is
// either an Ident or a QualifiedName, plus whether it succeeded.
func colRefName(e PS.Expr) (string, bool) {
	switch v := e.(type) {
	case *PS.Ident:
		return v.Name, true
	case *PS.QualifiedName:
		return v.Table + "." + v.Name, true
	}
	return "", false
}

// extractLiteral returns the Go value from a literal expression node.
func extractLiteral(e PS.Expr) (any, bool) {
	switch v := e.(type) {
	case *PS.NumberLiteral:
		return v.Val, true
	case *PS.FloatLiteral:
		return v.Val, true
	case *PS.StringLiteral:
		return v.Val, true
	case *PS.BoolLiteral:
		return v.Val, true
	case *PS.NullLiteral:
		return nil, true
	default:
		return nil, false
	}
}
