// Package EX's explain.go renders an operator tree as a human-readable
// description. EXPLAIN is not part of the v1 MVP scope per
// design/ARCH.md but the implementation is retained in v1.1 because
// it is the primary debugging surface for the executor.
package EX

import (
	"fmt"
	"strings"
)

func explainOperator(op Operator, depth int) string {
	var b strings.Builder
	b.WriteString(strings.Repeat("  ", depth))
	b.WriteString(describeOp(op))
	if c, ok := op.(interface{ Child() Operator }); ok {
		child := c.Child()
		if child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(child, depth+1))
		}
		return b.String()
	}
	switch v := op.(type) {
	case *Filter:
		if v.child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.child, depth+1))
		}
	case *Project:
		if v.child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.child, depth+1))
		}
	case *Aggregate:
		if v.child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.child, depth+1))
		}
	case *Sort:
		if v.child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.child, depth+1))
		}
	case *Limit:
		if v.child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.child, depth+1))
		}
	case *Distinct:
		if v.child != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.child, depth+1))
		}
	case *NestedLoopJoin:
		if v.left != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.left, depth+1))
		}
		if v.right != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.right, depth+1))
		}
	case *Update:
		if v.iter != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.iter, depth+1))
		}
	case *Delete:
		if v.iter != nil {
			b.WriteByte('\n')
			b.WriteString(explainOperator(v.iter, depth+1))
		}
	}
	return b.String()
}

func describeOp(op Operator) string {
	switch v := op.(type) {
	case *SeqScan:
		return fmt.Sprintf("SeqScan(table=%s)", v.table)
	case *IndexScan:
		return fmt.Sprintf("IndexScan(table=%s idx=%s)", v.table, v.idx)
	case *NestedLoopJoin:
		return fmt.Sprintf("NestedLoopJoin(left=%s right=%s)", v.leftTbl, v.rightTbl)
	case *Filter:
		return "Filter"
	case *Project:
		return "Project"
	case *Sort:
		return "Sort"
	case *Limit:
		return "Limit"
	case *Distinct:
		return "Distinct"
	case *Aggregate:
		return "Aggregate"
	case *Insert:
		return fmt.Sprintf("Insert(table=%s rows=%d)", v.table, len(v.values))
	case *Update:
		return fmt.Sprintf("Update(table=%s)", v.table)
	case *Delete:
		return fmt.Sprintf("Delete(table=%s)", v.table)
	case *CreateTable:
		return fmt.Sprintf("CreateTable(name=%s)", v.stmt.Name)
	case *DropTable:
		return fmt.Sprintf("DropTable(name=%s)", v.stmt.Name)
	}
	return "Unknown"
}
