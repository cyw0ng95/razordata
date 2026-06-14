package RE

import (
	"fmt"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type Result struct {
	SQL  string
	Args []any
}

func Format(stmt PS.Stmt) (string, error) {
	switch s := stmt.(type) {
	case *PS.Select:
		return formatSelect(s), nil
	case *PS.CompoundStmt:
		return formatCompound(s)
	case *PS.Insert:
		return formatInsertStmt(s), nil
	case *PS.Update:
		return formatUpdateStmt(s), nil
	case *PS.Delete:
		return formatDeleteStmt(s), nil
	case *PS.CreateTable:
		return formatCreateTableStmt(s), nil
	case *PS.DropTable:
		return formatDropTableStmt(s), nil
	case *PS.TriggerStmt:
		return formatTriggerStmt(s), nil
	case *PS.WithStmt:
		return formatWithStmt(s), nil
	case *PS.AlterTableStmt:
		return formatAlterTableStmt(s), nil
	}
	return "", fmt.Errorf("re: unknown statement type %T", stmt)
}

func formatSelect(s *PS.Select) string {
	var b strings.Builder
	b.WriteString("SELECT ")
	if s.Distinct {
		b.WriteString("DISTINCT ")
	}
	if len(s.Cols) == 0 {
		b.WriteString("*")
	} else {
		for i, col := range s.Cols {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(exprString(col))
		}
	}
	if s.From != "" {
		b.WriteString(" FROM ")
		b.WriteString(s.From)
		if s.FromAlias != "" {
			b.WriteString(" AS ")
			b.WriteString(s.FromAlias)
		}
	}
	if s.Where != nil {
		b.WriteString(" WHERE ")
		b.WriteString(exprString(s.Where))
	}
	if len(s.OrderBy) > 0 {
		b.WriteString(" ORDER BY ")
		for i, o := range s.OrderBy {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(exprString(o.Expr))
			if o.Desc {
				b.WriteString(" DESC")
			}
		}
	}
	if s.Limit != nil {
		b.WriteString(" LIMIT ")
		b.WriteString(exprString(s.Limit))
	}
	if s.Offset != nil {
		b.WriteString(" OFFSET ")
		b.WriteString(exprString(s.Offset))
	}
	return b.String()
}

func formatInsertStmt(i *PS.Insert) string {
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	b.WriteString(i.Table)
	if len(i.Cols) > 0 {
		b.WriteString(" (")
		for j, col := range i.Cols {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString(col)
		}
		b.WriteString(")")
	}
	b.WriteString(" VALUES ")
	for j, row := range i.Values {
		if j > 0 {
			b.WriteString(", ")
		}
		b.WriteString("(")
		for k, val := range row {
			if k > 0 {
				b.WriteString(", ")
			}
			b.WriteString(exprString(val))
		}
		b.WriteString(")")
	}
	return b.String()
}

func formatUpdateStmt(u *PS.Update) string {
	var b strings.Builder
	b.WriteString("UPDATE ")
	b.WriteString(u.Table)
	b.WriteString(" SET ")
	for i, pair := range u.Set {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(pair.Col)
		b.WriteString(" = ")
		b.WriteString(exprString(pair.Val))
	}
	if u.Where != nil {
		b.WriteString(" WHERE ")
		b.WriteString(exprString(u.Where))
	}
	return b.String()
}

func formatDeleteStmt(d *PS.Delete) string {
	var b strings.Builder
	b.WriteString("DELETE FROM ")
	b.WriteString(d.Table)
	if d.Where != nil {
		b.WriteString(" WHERE ")
		b.WriteString(exprString(d.Where))
	}
	return b.String()
}

func formatCreateTableStmt(c *PS.CreateTable) string {
	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	b.WriteString(c.Name)
	b.WriteString(" (")
	for i, col := range c.Cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(col.Name)
		b.WriteString(" ")
		b.WriteString(typeName(col.Type))
		if col.PK {
			b.WriteString(" PRIMARY KEY")
		}
		if !col.Nullable {
			b.WriteString(" NOT NULL")
		}
	}
	b.WriteString(")")
	return b.String()
}

func typeName(t int) string {
	switch t {
	case int(LX.T_INT_KW):
		return "INTEGER"
	case int(LX.T_BIGINT):
		return "BIGINT"
	case int(LX.T_FLOAT_KW):
		return "FLOAT"
	case int(LX.T_BOOL):
		return "BOOLEAN"
	case int(LX.T_TEXT):
		return "TEXT"
	case int(LX.T_BLOB):
		return "BLOB"
	case int(LX.T_VARCHAR):
		return "VARCHAR"
	case int(LX.T_TIMESTAMP):
		return "TIMESTAMP"
	default:
		return "TEXT"
	}
}

func formatDropTableStmt(d *PS.DropTable) string {
	var b strings.Builder
	b.WriteString("DROP TABLE ")
	b.WriteString(d.Name)
	return b.String()
}

func exprString(e PS.Expr) string {
	switch expr := e.(type) {
	case *PS.NumberLiteral:
		return fmt.Sprintf("%d", expr.Val)
	case *PS.FloatLiteral:
		return fmt.Sprintf("%f", expr.Val)
	case *PS.StringLiteral:
		return fmt.Sprintf("'%s'", expr.Val)
	case *PS.BoolLiteral:
		if expr.Val {
			return "TRUE"
		}
		return "FALSE"
	case *PS.NullLiteral:
		return "NULL"
	case *PS.Ident:
		return expr.Name
	case *PS.AliasedExpr:
		return fmt.Sprintf("%s AS %s", exprString(expr.Expr), expr.Alias)
	case *PS.Param:
		return "?"
	case *PS.StarExpr:
		return "*"
	case *PS.BinaryExpr:
		return fmt.Sprintf("(%s %s %s)", exprString(expr.Left), opString(expr.Op), exprString(expr.Right))
	case *PS.UnaryExpr:
		op := opString(expr.Op)
		operand := exprString(expr.Operand)
		if op == "NOT" || op == "AND" || op == "OR" {
			return fmt.Sprintf("%s %s", op, operand)
		}
		return fmt.Sprintf("%s%s", op, operand)
	case *PS.FunctionCall:
		var b strings.Builder
		b.WriteString(expr.Name)
		b.WriteString("(")
		for i, arg := range expr.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(exprString(arg))
		}
		b.WriteString(")")
		return b.String()
	case *PS.AggregateFunc:
		var b strings.Builder
		b.WriteString(expr.Name)
		b.WriteString("(")
		b.WriteString(exprString(expr.Arg))
		b.WriteString(")")
		return b.String()
	case *PS.CastExpr:
		if expr.Type == nil {
			return fmt.Sprintf("CAST(%s AS UNKNOWN)", exprString(expr.Expr))
		}
		return fmt.Sprintf("CAST(%s AS %s)", exprString(expr.Expr), typeName(expr.Type.Type))
	case *PS.ListExpr:
		var b strings.Builder
		b.WriteString("(")
		for i, item := range expr.Items {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(exprString(item))
		}
		b.WriteString(")")
		return b.String()
	case *PS.BetweenExpr:
		return fmt.Sprintf("%s BETWEEN %s AND %s", exprString(expr.Expr), exprString(expr.Low), exprString(expr.High))
	case *PS.CaseExpr:
		var b strings.Builder
		b.WriteString("CASE")
		if expr.Expr != nil {
			b.WriteString(" ")
			b.WriteString(exprString(expr.Expr))
		}
		for _, w := range expr.WhenList {
			b.WriteString(" WHEN ")
			b.WriteString(exprString(w.Cond))
			b.WriteString(" THEN ")
			b.WriteString(exprString(w.Then))
		}
		if expr.Else != nil {
			b.WriteString(" ELSE ")
			b.WriteString(exprString(expr.Else))
		}
		b.WriteString(" END")
		return b.String()
	}
	return "?"
}

func opString(op int) string {
	switch LX.TokenType(op) {
	case LX.T_EQ:
		return "="
	case LX.T_NE:
		return "<>"
	case LX.T_LT:
		return "<"
	case LX.T_LE:
		return "<="
	case LX.T_GT:
		return ">"
	case LX.T_GE:
		return ">="
	case LX.T_PLUS:
		return "+"
	case LX.T_MINUS:
		return "-"
	case LX.T_STAR:
		return "*"
	case LX.T_SLASH:
		return "/"
	case LX.T_AND:
		return "AND"
	case LX.T_OR:
		return "OR"
	case LX.T_NOT:
		return "NOT"
	case LX.T_IN:
		return "IN"
	case LX.T_BETWEEN:
		return "BETWEEN"
	case LX.T_LIKE:
		return "LIKE"
	case LX.T_IS:
		return "IS"
	default:
		return "?"
	}
}

// formatCompound formats a UNION/INTERSECT/EXCEPT chain. REQ000383.
func formatCompound(s *PS.CompoundStmt) (string, error) {
	left, err := Format(s.Left)
	if err != nil {
		return "", err
	}
	right, err := Format(s.Right)
	if err != nil {
		return "", err
	}
	var op string
	switch s.Op {
	case PS.CompoundUnion:
		op = " UNION "
	case PS.CompoundUnionAll:
		op = " UNION ALL "
	case PS.CompoundIntersect:
		op = " INTERSECT "
	case PS.CompoundExcept:
		op = " EXCEPT "
	}
	out := left + op + right
	if len(s.OrderBy) > 0 {
		parts := make([]string, 0, len(s.OrderBy))
		for _, o := range s.OrderBy {
			dir := "ASC"
			if o.Desc {
				dir = "DESC"
			}
			parts = append(parts, fmt.Sprintf("%s %s", exprString(o.Expr), dir))
		}
		out += " ORDER BY " + strings.Join(parts, ", ")
	}
	if s.Limit != nil {
		out += " LIMIT " + exprString(s.Limit)
	}
	if s.Offset != nil {
		out += " OFFSET " + exprString(s.Offset)
	}
	return out, nil
}

func formatTriggerStmt(t *PS.TriggerStmt) string {
	var b strings.Builder
	b.WriteString("CREATE TRIGGER ")
	b.WriteString(t.Name)
	b.WriteString(" ")
	b.WriteString(t.Time)
	b.WriteString(" ")
	b.WriteString(t.Event)
	b.WriteString(" ON ")
	b.WriteString(t.OnTable)
	b.WriteString(" BEGIN\n")
	for _, s := range t.Body {
		out, _ := Format(s)
		b.WriteString(out)
		b.WriteString(";\n")
	}
	b.WriteString("END")
	return b.String()
}

func formatWithStmt(w *PS.WithStmt) string {
	var b strings.Builder
	b.WriteString("WITH ")
	if w.Recursive {
		b.WriteString("RECURSIVE ")
	}
	for i, cte := range w.CTEs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(cte.Name)
		if len(cte.Cols) > 0 {
			b.WriteString("(")
			for j, c := range cte.Cols {
				if j > 0 {
					b.WriteString(", ")
				}
				b.WriteString(c)
			}
			b.WriteString(")")
		}
		b.WriteString(" AS (")
		out, _ := Format(cte.Query)
		b.WriteString(out)
		b.WriteString(")")
	}
	b.WriteString(" ")
	out, _ := Format(w.Inner)
	b.WriteString(out)
	return b.String()
}

func formatAlterTableStmt(a *PS.AlterTableStmt) string {
	return "ALTER TABLE " + a.Table + " " + a.Action
}
