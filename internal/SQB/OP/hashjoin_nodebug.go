//go:build !debug

package OP

func hashJoinDebugRowFlow(table string, rowID uint64, entering bool) {}
func hashJoinDebugPredicate(expr string, leftRowID, rightRowID uint64, passed bool) {
}
func hashJoinDebugStrategy(chosen, reason string, cost float64) {}
func hashJoinDebugCorrelation(stage int, tables []string, rowCount int64) {
}
