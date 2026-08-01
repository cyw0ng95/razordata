package QP

import (
	"errors"

	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// BuildQueryPlan constructs a QueryPlan DAG directly from a parsed PS.Stmt.
// This is the foundation entry point REQ002267 needs: DML (INSERT/UPDATE/
// DELETE) is represented as DAG nodes alongside the read operators, so later
// phases can route the whole plan through the PipelineExecutor.
//
// The builder reads only exported AST fields (no OP/WT imports), so it is a
// clean seam between parsing and planning. It currently covers the core shapes
// (scan / filter / project / join / limit-offset / distinct) and the DML
// statements; compound statements reuse the per-side builders recursively.
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
			Op:       OpCompound,
			Children: []*PlanNode{left, right},
		}, nil
	default:
		return nil, errors.New("QP: unsupported statement kind")
	}
}

func buildSelect(s *PS.Select) (*PlanNode, error) {
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

	// GROUP BY / HAVING captured on the projection node below.

	// Projection.
	proj := &PlanNode{
		Op:       OpProject,
		Exprs:    s.Cols,
		Alias:    s.FromAlias,
		GroupBy:  s.GroupBy,
		Having:   s.Having,
		Distinct: s.Distinct,
	}
	proj.AddChild(child)
	child = proj

	// ORDER BY → sort (preserve as a marker node).
	if len(s.OrderBy) > 0 {
		child = NewSort(child)
	}

	// LIMIT / OFFSET.
	if s.Limit != nil || s.Offset != nil {
		child = NewLimit(child, s.Limit, s.Offset)
	}

	return child, nil
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
	var source *PlanNode
	if s.Select != nil {
		sub, err := buildStmt(s.Select)
		if err != nil {
			return nil, err
		}
		source = sub
	}
	return NewInsert(s.Table, s.Cols, s.Values, source, s.Returning), nil
}

func buildUpdate(s *PS.Update) (*PlanNode, error) {
	scan := NewSeqScan(s.Table, "", nil)
	return NewUpdate(s.Table, s.Set, s.Where, scan, s.Returning), nil
}

func buildDelete(s *PS.Delete) (*PlanNode, error) {
	scan := NewSeqScan(s.Table, "", nil)
	return NewDelete(s.Table, s.Where, scan, s.Returning), nil
}
