package JN

import (
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// StatsProvider supplies table statistics for join ordering.
type StatsProvider interface {
	TableRowCount(table string) float64
	HasIndex(table string) bool
	JoinPredSel(pred PS.Expr, rowCount float64) float64
	JoinCost(leftRows, rightRows int, predicates []PS.Expr, hasIndex bool) float64
	JoinResultRows(leftRows, rightRows float64, predicates []PS.Expr) float64
	CanPushDown(pred PS.Expr, table string) bool
	ExtractTablesFromExpr(e PS.Expr) map[string]bool
	FindTableForColumn(col string) string
}

// JoinTableInfo holds a table name and its associated JoinClause.
type JoinTableInfo struct {
	Name string
	Join PS.JoinClause
}

// BushyGroup represents a group of independent equi-joins.
type BushyGroup struct {
	Tables []string
	Joins  []PS.JoinClause
}

const (
	N3HeapMaxSize      = 24
	N3PruneMultiplier  = 2.0
)