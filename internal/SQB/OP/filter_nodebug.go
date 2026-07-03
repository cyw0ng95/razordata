//go:build !debug

package OP

func filterDebugPredicate(expr string, rowID uint64, passed bool) {}
