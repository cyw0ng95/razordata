//go:build !debug

package OP

func nljDebugRowFlow(table string, rowID uint64, entering bool)       {}
func nljDebugPredicate(expr string, leftRowID, rightRowID uint64, passed bool) {}
func nljDebugStrategy(chosen, reason string, cost float64)              {}
func nljDebugCorrelation(stage int, tables []string, rowCount int64)    {}
