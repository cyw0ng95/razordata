package RE

import (
	RW "github.com/cyw0ng95/razordata/internal/SQO/RW"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func Rewrite(stmt PS.Stmt) (PS.Stmt, error) {
	return RW.Rewrite(stmt)
}

func SplitAnd(e PS.Expr) []PS.Expr {
	return RW.SplitAnd(e)
}