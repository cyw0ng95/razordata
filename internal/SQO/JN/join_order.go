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

// N3HeapMaxSizeForJoins returns the heap size for the N3 algorithm
// based on the join count K. For small joins (≤4), use a smaller
// heap (8); for larger joins, use the default 24. REQ001504.
func N3HeapMaxSizeForJoins(K int) int {
	if K <= 4 {
		return 8
	}
	return N3HeapMaxSize
}