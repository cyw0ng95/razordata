package QP

import (
	"errors"

	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// BuildQueryPlan constructs a QueryPlan DAG directly from a parsed PS.Stmt.
// This is the foundation entry point REQ002267 needs: DML (INSERT/UPDATE/
// DELETE) is represented as DAG nodes alongside the read operators, so the
// PipelineExecutor can route the whole plan through the DAG.
//
// The builder reads only exported AST fields (no OP/WT imports). It models
// every structural shape (scan / filter / project / join / sort / limit /
// offset / distinct / values / compound / DML). For shapes the execution
// machinery cannot lower faithfully yet (aggregates, window functions,
// expression subqueries, UPDATE/DELETE with ORDER BY/LIMIT, INSERT DEFAULT
// VALUES, subquery-in-FROM), it returns an error so the caller falls back to
// the legacy operator path. This keeps the QP-routed set correct while the
// DAG still *represents* every shape.
func BuildQueryPlan(stmt PS.Stmt) (*QueryPlan, error) {
	if stmt == nil {
		return nil, errors.New("QP: nil statement")
	}
	root, err := buildStmt(stmt)
	if err != nil {
		return nil, err
	}
	return &QueryPlan{Root: root}, nil
}

func buildStmt(stmt PS.Stmt) (*PlanNode, error) {
	switch s := stmt.(type) {
	case *PS.Select:
		return buildSelect(s)
	case *PS.Insert:
		return buildInsert(s)
	case *PS.Update:
		return buildUpdate(s)
	case *PS.Delete:
		return buildDelete(s)
	case *PS.CompoundStmt:
		// Build each side recursively; the compound node carries both roots.
		left, err := buildStmt(s.Left)
		if err != nil {
			return nil, err
		}
		right, err := buildStmt(s.Right)
		if err != nil {
			return nil, err
		}
		return &PlanNode{
			Op:         OpCompound,
			Children:   []*PlanNode{left, right},
			CompoundOp: s.Op,
		}, nil
	default:
		return nil, errors.New("QP: unsupported statement kind")
	}
}

func buildSelect(s *PS.Select) (*PlanNode, error) {
	// Shapes we cannot lower faithfully to the vectorized pipeline yet.
	if selectHasUnsupported(s) {
		return nil, errors.New("QP: select has unsupported feature")
	}

	var child *PlanNode

	// FROM source: a subquery, a single table, or a join chain.
	switch {
	case s.SubqueryFrom != nil:
		sub, err := buildStmt(s.SubqueryFrom)
		if err != nil {
			return nil, err
		}
		child = sub
	case s.From != "":
		child = NewSeqScan(s.From, s.FromAlias, nil)
		if len(s.Joins) > 0 {
			jn, err := buildJoins(child, s.Joins)
			if err != nil {
				return nil, err
			}
			child = jn
		}
	default:
		// SELECT without FROM (e.g. SELECT 1) — constant row.
		child = &PlanNode{Op: OpConstRow}
	}

	// WHERE → filter.
	if s.Where != nil {
		child = NewFilter(child, s.Where)
	}

	// Projection.
	proj := &PlanNode{
		Op:       OpProject,
		Exprs:    s.Cols,
		Alias:    s.FromAlias,
		Distinct: s.Distinct,
	}
	proj.AddChild(child)
	child = proj

	// ORDER BY → sort.
	if len(s.OrderBy) > 0 {
		sort := &PlanNode{Op: OpSort, OrderBy: s.OrderBy}
		sort.AddChild(child)
		child = sort
	}

	// LIMIT / OFFSET.
	if s.Limit != nil || s.Offset != nil {
		child = NewLimit(child, s.Limit, s.Offset)
	}

	return child, nil
}

// selectHasUnsupported reports whether s contains a feature the execution
// path cannot lower faithfully (so the caller falls back to the OP tree).
// Joins are allowed structurally (modeled as OpHashJoin) but lowered to OP
// at execution time; they are intentionally NOT listed here.
func selectHasUnsupported(s *PS.Select) bool {
	if s.SubqueryFrom != nil {
		return true
	}
	if s.GroupBy != nil || s.Having != nil {
		return true
	}
	for _, e := range s.Cols {
		if exprHasUnsupported(e) {
			return true
		}
	}
	if exprHasUnsupported(s.Where) {
		return true
	}
	for _, o := range s.OrderBy {
		if exprHasUnsupported(o.Expr) {
			return true
		}
	}
	for _, j := range s.Joins {
		if exprHasUnsupported(j.On) {
			return true
		}
	}
	return false
}

// exprHasUnsupported reports aggregate, window, or subquery expressions,
// which the current QP execution path cannot lower correctly.
func exprHasUnsupported(e PS.Expr) bool {
	if e == nil {
		return false
	}
	switch e.(type) {
	case *PS.AggregateFunc, *PS.WindowFunc, *PS.SubqueryExpr, *PS.ExistsExpr:
		return true
	case *PS.InExpr:
		if in, ok := e.(*PS.InExpr); ok && in.Subquery != nil {
			return true
		}
	}
	switch x := e.(type) {
	case *PS.BinaryExpr:
		return exprHasUnsupported(x.Left) || exprHasUnsupported(x.Right) || exprHasUnsupported(x.Escape)
	case *PS.UnaryExpr:
		return exprHasUnsupported(x.Operand)
	case *PS.FunctionCall:
		for _, a := range x.Args {
			if exprHasUnsupported(a) {
				return true
			}
		}
	case *PS.AliasedExpr:
		return exprHasUnsupported(x.Expr)
	case *PS.CastExpr:
		return exprHasUnsupported(x.Expr)
	case *PS.ListExpr:
		for _, it := range x.Items {
			if exprHasUnsupported(it) {
				return true
			}
		}
	case *PS.BetweenExpr:
		return exprHasUnsupported(x.Expr) || exprHasUnsupported(x.Low) || exprHasUnsupported(x.High)
	case *PS.CaseExpr:
		if exprHasUnsupported(x.Expr) {
			return true
		}
		for _, w := range x.WhenList {
			if exprHasUnsupported(w.Cond) || exprHasUnsupported(w.Then) {
				return true
			}
		}
		return exprHasUnsupported(x.Else)
	}
	return false
}

func buildJoins(left *PlanNode, joins []PS.JoinClause) (*PlanNode, error) {
	cur := left
	for _, j := range joins {
		right := NewSeqScan(j.Right, j.RightAlias, nil)
		node := NewHashJoin(cur, right, j.On, cur.Alias, j.RightAlias)
		node.JoinKind = joinKindCode(j.Kind)
		cur = node
	}
	return cur, nil
}

func joinKindCode(kind string) int {
	switch kind {
	case "LEFT", "LEFT OUTER":
		return 1
	case "RIGHT", "RIGHT OUTER":
		return 2
	case "FULL", "FULL OUTER":
		return 3
	case "CROSS":
		return 4
	default:
		return 0 // INNER
	}
}

func buildInsert(s *PS.Insert) (*PlanNode, error) {
	// INSERT DEFAULT VALUES takes a separate WT path the builder cannot
	// model; fall back to the OP planner.
	if s.DefaultValues {
		return nil, errors.New("QP: INSERT DEFAULT VALUES unsupported")
	}
	var source *PlanNode
	if s.Select != nil {
		sub, err := buildStmt(s.Select)
		if err != nil {
			return nil, err
		}
		source = sub
	}
	return NewInsert(s.Table, s.Cols, s.Values, source, s.Returning, s.OnConflict, s.ConflictAction), nil
}

func buildUpdate(s *PS.Update) (*PlanNode, error) {
	// UPDATE ... ORDER BY / LIMIT cannot be represented faithfully yet.
	if s.OrderBy != nil || s.Limit != nil || s.Offset != nil {
		return nil, errors.New("QP: UPDATE with ORDER BY/LIMIT unsupported")
	}
	// WT.Update/Delete apply the mutation to EVERY row from their iter and
	// do NOT evaluate `where` themselves — the planner pre-filters the iter
	// via a SeqScan+Filter chain. Mirror that: wrap the scan in a Filter so
	// only matching rows reach the writer. `where` is still passed through
	// to WT (matching the planner) for RETURNING / store-path semantics.
	scan := NewSeqScan(s.Table, "", nil)
	if s.Where != nil {
		scan = NewFilter(scan, s.Where)
	}
	return NewUpdate(s.Table, s.Set, s.Where, scan, s.Returning), nil
}

func buildDelete(s *PS.Delete) (*PlanNode, error) {
	if s.OrderBy != nil || s.Limit != nil || s.Offset != nil {
		return nil, errors.New("QP: DELETE with ORDER BY/LIMIT unsupported")
	}
	scan := NewSeqScan(s.Table, "", nil)
	if s.Where != nil {
		scan = NewFilter(scan, s.Where)
	}
	return NewDelete(s.Table, s.Where, scan, s.Returning), nil
}
