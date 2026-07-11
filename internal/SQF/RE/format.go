package RE

import (
	RW "github.com/cyw0ng95/razordata/internal/SQO/RW"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func Format(stmt PS.Stmt) (string, error) { return RW.Format(stmt) }
func FormatExpr(e PS.Expr) string         { return RW.FormatExpr(e) }