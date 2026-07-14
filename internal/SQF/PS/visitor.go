package PS

// Visitor is the combined AST traversal interface. It has one
// method per concrete Expr and Stmt node type so a pass can
// dispatch without type-switching. The default AcceptExpr /
// AcceptStmt functions call the matching Visit method and
// recurse into children, returning false on early exit.
//
// REQ001433: scaffold. No callers in this commit. Passes are
// added in REQ001448 (SubqueryDecorrelation) and beyond.
// Purely additive.
type Visitor interface {
	ExprVisitor
	StmtVisitor
}

// ExprVisitor has one method per Expr node type. StmtVisitor
// has one method per Stmt node type. A visitor that wants to
// walk only expressions or only statements embeds BaseVisitor
// (which provides all methods) and overrides the ones it cares
// about. The combined Visitor interface lets one struct handle
// both (see testVisitor in visitor_test.go).
type ExprVisitor interface {
	VisitNumberLiteral(*NumberLiteral) bool
	VisitFloatLiteral(*FloatLiteral) bool
	VisitStringLiteral(*StringLiteral) bool
	VisitBoolLiteral(*BoolLiteral) bool
	VisitNullLiteral(*NullLiteral) bool
	VisitIdent(*Ident) bool
	VisitQualifiedName(*QualifiedName) bool
	VisitAliasedExpr(*AliasedExpr) bool
	VisitCastExpr(*CastExpr) bool
	VisitParam(*Param) bool
	VisitBinaryExpr(*BinaryExpr) bool
	VisitUnaryExpr(*UnaryExpr) bool
	VisitFunctionCall(*FunctionCall) bool
	VisitAggregateFunc(*AggregateFunc) bool
	VisitWindowFunc(*WindowFunc) bool
	VisitStarExpr(*StarExpr) bool
	VisitListExpr(*ListExpr) bool
	VisitBetweenExpr(*BetweenExpr) bool
	VisitCaseExpr(*CaseExpr) bool
	VisitInExpr(*InExpr) bool
	VisitExistsExpr(*ExistsExpr) bool
	VisitSubqueryExpr(*SubqueryExpr) bool
	VisitIntervalLiteral(*IntervalLiteral) bool
	VisitRaiseFunc(*RaiseFunc) bool
}

type StmtVisitor interface {
	VisitCreateTable(*CreateTable) bool
	VisitDropTable(*DropTable) bool
	VisitInsert(*Insert) bool
	VisitUpdate(*Update) bool
	VisitDelete(*Delete) bool
	VisitSelect(*Select) bool
	VisitCompoundStmt(*CompoundStmt) bool
	VisitBeginTX(*BeginTX) bool
	VisitCommitTX(*CommitTX) bool
	VisitRollbackTX(*RollbackTX) bool
	VisitExplainStmt(*ExplainStmt) bool
	VisitCreateIndexStmt(*CreateIndexStmt) bool
	VisitDropIndexStmt(*DropIndexStmt) bool
	VisitCreateViewStmt(*CreateViewStmt) bool
	VisitAlterTableStmt(*AlterTableStmt) bool
	VisitPragmaStmt(*PragmaStmt) bool
	VisitAnalyzeStmt(*AnalyzeStmt) bool
	VisitVacuumStmt(*VacuumStmt) bool
	VisitTriggerStmt(*TriggerStmt) bool
	VisitSavepointStmt(*SavepointStmt) bool
	VisitReleaseSavepointStmt(*ReleaseSavepointStmt) bool
	VisitRollbackToStmt(*RollbackToStmt) bool
	VisitWithStmt(*WithStmt) bool
	VisitTruncateStmt(*TruncateStmt) bool
	VisitReindexStmt(*ReindexStmt) bool
	VisitDropViewStmt(*DropViewStmt) bool
	VisitDropTriggerStmt(*DropTriggerStmt) bool
	VisitCreateMatViewStmt(*CreateMatViewStmt) bool
	VisitDropMatViewStmt(*DropMatViewStmt) bool
	VisitRefreshMatViewStmt(*RefreshMatViewStmt) bool
	VisitSetTransactionStmt(*SetTransactionStmt) bool
	VisitValuesStmt(*ValuesStmt) bool
	VisitAttachStmt(*AttachStmt) bool
	VisitDetachStmt(*DetachStmt) bool
}

// BaseVisitor is the default no-op implementation. Embed it in a
// visitor type to inherit the default `return true` for any
// method you don't override.
//
// Idiomatic use:
//
//	type findColumns struct {
//	    PS.BaseVisitor
//	    cols []string
//	}
//
//	func (v *findColumns) VisitIdent(i *PS.Ident) bool {
//	    v.cols = append(v.cols, i.Name)
//	    return true
//	}
type BaseVisitor struct{}

func (BaseVisitor) VisitNumberLiteral(*NumberLiteral) bool   { return true }
func (BaseVisitor) VisitFloatLiteral(*FloatLiteral) bool     { return true }
func (BaseVisitor) VisitStringLiteral(*StringLiteral) bool   { return true }
func (BaseVisitor) VisitBoolLiteral(*BoolLiteral) bool      { return true }
func (BaseVisitor) VisitNullLiteral(*NullLiteral) bool      { return true }
func (BaseVisitor) VisitIdent(*Ident) bool                  { return true }
func (BaseVisitor) VisitQualifiedName(*QualifiedName) bool  { return true }
func (BaseVisitor) VisitAliasedExpr(*AliasedExpr) bool      { return true }
func (BaseVisitor) VisitCastExpr(*CastExpr) bool            { return true }
func (BaseVisitor) VisitParam(*Param) bool                  { return true }
func (BaseVisitor) VisitBinaryExpr(*BinaryExpr) bool        { return true }
func (BaseVisitor) VisitUnaryExpr(*UnaryExpr) bool          { return true }
func (BaseVisitor) VisitFunctionCall(*FunctionCall) bool    { return true }
func (BaseVisitor) VisitAggregateFunc(*AggregateFunc) bool  { return true }
func (BaseVisitor) VisitWindowFunc(*WindowFunc) bool        { return true }
func (BaseVisitor) VisitStarExpr(*StarExpr) bool            { return true }
func (BaseVisitor) VisitListExpr(*ListExpr) bool            { return true }
func (BaseVisitor) VisitBetweenExpr(*BetweenExpr) bool      { return true }
func (BaseVisitor) VisitCaseExpr(*CaseExpr) bool            { return true }
func (BaseVisitor) VisitInExpr(*InExpr) bool                { return true }
func (BaseVisitor) VisitExistsExpr(*ExistsExpr) bool        { return true }
func (BaseVisitor) VisitSubqueryExpr(*SubqueryExpr) bool    { return true }
func (BaseVisitor) VisitIntervalLiteral(*IntervalLiteral) bool {
	return true
}
func (BaseVisitor) VisitRaiseFunc(*RaiseFunc) bool { return true }

func (BaseVisitor) VisitCreateTable(*CreateTable) bool                { return true }
func (BaseVisitor) VisitDropTable(*DropTable) bool                    { return true }
func (BaseVisitor) VisitInsert(*Insert) bool                          { return true }
func (BaseVisitor) VisitUpdate(*Update) bool                          { return true }
func (BaseVisitor) VisitDelete(*Delete) bool                          { return true }
func (BaseVisitor) VisitSelect(*Select) bool                          { return true }
func (BaseVisitor) VisitCompoundStmt(*CompoundStmt) bool              { return true }
func (BaseVisitor) VisitBeginTX(*BeginTX) bool                        { return true }
func (BaseVisitor) VisitCommitTX(*CommitTX) bool                      { return true }
func (BaseVisitor) VisitRollbackTX(*RollbackTX) bool                  { return true }
func (BaseVisitor) VisitExplainStmt(*ExplainStmt) bool                { return true }
func (BaseVisitor) VisitCreateIndexStmt(*CreateIndexStmt) bool        { return true }
func (BaseVisitor) VisitDropIndexStmt(*DropIndexStmt) bool            { return true }
func (BaseVisitor) VisitCreateViewStmt(*CreateViewStmt) bool          { return true }
func (BaseVisitor) VisitAlterTableStmt(*AlterTableStmt) bool          { return true }
func (BaseVisitor) VisitPragmaStmt(*PragmaStmt) bool                  { return true }
func (BaseVisitor) VisitAnalyzeStmt(*AnalyzeStmt) bool                { return true }
func (BaseVisitor) VisitVacuumStmt(*VacuumStmt) bool                  { return true }
func (BaseVisitor) VisitTriggerStmt(*TriggerStmt) bool                { return true }
func (BaseVisitor) VisitSavepointStmt(*SavepointStmt) bool            { return true }
func (BaseVisitor) VisitReleaseSavepointStmt(*ReleaseSavepointStmt) bool {
	return true
}
func (BaseVisitor) VisitRollbackToStmt(*RollbackToStmt) bool     { return true }
func (BaseVisitor) VisitWithStmt(*WithStmt) bool                 { return true }
func (BaseVisitor) VisitTruncateStmt(*TruncateStmt) bool         { return true }
func (BaseVisitor) VisitReindexStmt(*ReindexStmt) bool           { return true }
func (BaseVisitor) VisitDropViewStmt(*DropViewStmt) bool         { return true }
func (BaseVisitor) VisitDropTriggerStmt(*DropTriggerStmt) bool   { return true }
func (BaseVisitor) VisitCreateMatViewStmt(*CreateMatViewStmt) bool {
	return true
}
func (BaseVisitor) VisitDropMatViewStmt(*DropMatViewStmt) bool { return true }
func (BaseVisitor) VisitRefreshMatViewStmt(*RefreshMatViewStmt) bool {
	return true
}
func (BaseVisitor) VisitSetTransactionStmt(*SetTransactionStmt) bool {
	return true
}
func (BaseVisitor) VisitValuesStmt(*ValuesStmt) bool { return true }
func (BaseVisitor) VisitAttachStmt(*AttachStmt) bool { return true }
func (BaseVisitor) VisitDetachStmt(*DetachStmt) bool { return true }

// Compile-time checks: BaseVisitor satisfies all three interfaces.
var (
	_ ExprVisitor = BaseVisitor{}
	_ StmtVisitor = BaseVisitor{}
	_ Visitor     = BaseVisitor{}
)

// AcceptExpr visits the expression and recurses into children.
// Returns false if the visitor returns false at any point (early
// exit), true otherwise. AcceptExpr(nil, v) returns true (no-op).
func AcceptExpr(e Expr, v ExprVisitor) bool {
	if e == nil {
		return true
	}
	switch n := e.(type) {
	case *NumberLiteral:
		if !v.VisitNumberLiteral(n) {
			return false
		}
	case *FloatLiteral:
		if !v.VisitFloatLiteral(n) {
			return false
		}
	case *StringLiteral:
		if !v.VisitStringLiteral(n) {
			return false
		}
	case *BoolLiteral:
		if !v.VisitBoolLiteral(n) {
			return false
		}
	case *NullLiteral:
		if !v.VisitNullLiteral(n) {
			return false
		}
	case *Ident:
		if !v.VisitIdent(n) {
			return false
		}
	case *QualifiedName:
		if !v.VisitQualifiedName(n) {
			return false
		}
	case *AliasedExpr:
		if !v.VisitAliasedExpr(n) {
			return false
		}
		return AcceptExpr(n.Expr, v)
	case *CastExpr:
		if !v.VisitCastExpr(n) {
			return false
		}
		return AcceptExpr(n.Expr, v)
	case *Param:
		if !v.VisitParam(n) {
			return false
		}
	case *BinaryExpr:
		if !v.VisitBinaryExpr(n) {
			return false
		}
		if !AcceptExpr(n.Left, v) {
			return false
		}
		if !AcceptExpr(n.Right, v) {
			return false
		}
		return AcceptExpr(n.Escape, v)
	case *UnaryExpr:
		if !v.VisitUnaryExpr(n) {
			return false
		}
		return AcceptExpr(n.Operand, v)
	case *FunctionCall:
		if !v.VisitFunctionCall(n) {
			return false
		}
		for _, a := range n.Args {
			if !AcceptExpr(a, v) {
				return false
			}
		}
	case *AggregateFunc:
		if !v.VisitAggregateFunc(n) {
			return false
		}
		if !AcceptExpr(n.Arg, v) {
			return false
		}
		if !AcceptExpr(n.Separator, v) {
			return false
		}
		return AcceptExpr(n.Filter, v)
	case *WindowFunc:
		if !v.VisitWindowFunc(n) {
			return false
		}
		for _, a := range n.Args {
			if !AcceptExpr(a, v) {
				return false
			}
		}
	case *StarExpr:
		if !v.VisitStarExpr(n) {
			return false
		}
	case *ListExpr:
		if !v.VisitListExpr(n) {
			return false
		}
		for _, item := range n.Items {
			if !AcceptExpr(item, v) {
				return false
			}
		}
	case *BetweenExpr:
		if !v.VisitBetweenExpr(n) {
			return false
		}
		if !AcceptExpr(n.Expr, v) {
			return false
		}
		if !AcceptExpr(n.Low, v) {
			return false
		}
		return AcceptExpr(n.High, v)
	case *CaseExpr:
		if !v.VisitCaseExpr(n) {
			return false
		}
		if !AcceptExpr(n.Expr, v) {
			return false
		}
		for _, w := range n.WhenList {
			if !AcceptExpr(w.Cond, v) {
				return false
			}
			if !AcceptExpr(w.Then, v) {
				return false
			}
		}
		return AcceptExpr(n.Else, v)
	case *InExpr:
		if !v.VisitInExpr(n) {
			return false
		}
		if !AcceptExpr(n.Expr, v) {
			return false
		}
		for _, item := range n.List {
			if !AcceptExpr(item, v) {
				return false
			}
		}
	case *ExistsExpr:
		if !v.VisitExistsExpr(n) {
			return false
		}
	case *SubqueryExpr:
		if !v.VisitSubqueryExpr(n) {
			return false
		}
	case *IntervalLiteral:
		if !v.VisitIntervalLiteral(n) {
			return false
		}
	case *RaiseFunc:
		if !v.VisitRaiseFunc(n) {
			return false
		}
		return AcceptExpr(n.Message, v)
	}
	return true
}

// AcceptStmt visits the statement and recurses into child
// expressions. Returns false on early exit. AcceptStmt(nil, v)
// returns true (no-op). The visitor argument must implement
// Visitor (the combined Expr+Stmt interface) so the function
// can recurse into expressions from inside a statement body.
func AcceptStmt(s Stmt, v Visitor) bool {
	if s == nil {
		return true
	}
	switch n := s.(type) {
	case *CreateTable:
		if !v.VisitCreateTable(n) {
			return false
		}
	case *DropTable:
		if !v.VisitDropTable(n) {
			return false
		}
	case *Insert:
		if !v.VisitInsert(n) {
			return false
		}
		for _, row := range n.Values {
			for _, cell := range row {
				if !AcceptExpr(cell, v) {
					return false
				}
			}
		}
		for _, r := range n.Returning {
			if !AcceptExpr(r, v) {
				return false
			}
		}
		if n.OnConflict != nil {
			for _, sc := range n.OnConflict.SetClauses {
				if !AcceptExpr(sc.Val, v) {
					return false
				}
			}
			if !AcceptExpr(n.OnConflict.TargetWhere, v) {
				return false
			}
			if !AcceptExpr(n.OnConflict.UpdateWhere, v) {
				return false
			}
		}
		return AcceptStmt(n.Select, v)
	case *Update:
		if !v.VisitUpdate(n) {
			return false
		}
		if !AcceptExpr(n.Where, v) {
			return false
		}
		for _, a := range n.Set {
			if !AcceptExpr(a.Val, v) {
				return false
			}
		}
		for _, r := range n.Returning {
			if !AcceptExpr(r, v) {
				return false
			}
		}
		for _, ob := range n.OrderBy {
			if !AcceptExpr(ob.Expr, v) {
				return false
			}
		}
		return AcceptExpr(n.Limit, v)
	case *Delete:
		if !v.VisitDelete(n) {
			return false
		}
		if !AcceptExpr(n.Where, v) {
			return false
		}
		for _, r := range n.Returning {
			if !AcceptExpr(r, v) {
				return false
			}
		}
		return true
	case *Select:
		if !v.VisitSelect(n) {
			return false
		}
		for _, c := range n.Cols {
			if !AcceptExpr(c, v) {
				return false
			}
		}
		if !AcceptExpr(n.Where, v) {
			return false
		}
		for _, ob := range n.OrderBy {
			if !AcceptExpr(ob.Expr, v) {
				return false
			}
		}
		for _, gb := range n.GroupBy {
			if !AcceptExpr(gb, v) {
				return false
			}
		}
		if n.Having != nil {
			if !AcceptExpr(n.Having, v) {
				return false
			}
		}
		return true
	case *CompoundStmt:
		if !v.VisitCompoundStmt(n) {
			return false
		}
		if !AcceptStmt(n.Left, v) {
			return false
		}
		return AcceptStmt(n.Right, v)
	case *BeginTX:
		if !v.VisitBeginTX(n) {
			return false
		}
	case *CommitTX:
		if !v.VisitCommitTX(n) {
			return false
		}
	case *RollbackTX:
		if !v.VisitRollbackTX(n) {
			return false
		}
	case *ExplainStmt:
		if !v.VisitExplainStmt(n) {
			return false
		}
		return AcceptStmt(n.Inner, v)
	case *CreateIndexStmt:
		if !v.VisitCreateIndexStmt(n) {
			return false
		}
		return AcceptExpr(n.Where, v)
	case *DropIndexStmt:
		if !v.VisitDropIndexStmt(n) {
			return false
		}
	case *CreateViewStmt:
		if !v.VisitCreateViewStmt(n) {
			return false
		}
		return AcceptStmt(n.As, v)
	case *AlterTableStmt:
		if !v.VisitAlterTableStmt(n) {
			return false
		}
		return AcceptExpr(n.NewExpr, v)
	case *PragmaStmt:
		if !v.VisitPragmaStmt(n) {
			return false
		}
	case *AnalyzeStmt:
		if !v.VisitAnalyzeStmt(n) {
			return false
		}
	case *VacuumStmt:
		if !v.VisitVacuumStmt(n) {
			return false
		}
	case *TriggerStmt:
		if !v.VisitTriggerStmt(n) {
			return false
		}
		if !AcceptExpr(n.When, v) {
			return false
		}
		for _, a := range n.Body {
			if !AcceptStmt(a, v) {
				return false
			}
		}
	case *SavepointStmt:
		if !v.VisitSavepointStmt(n) {
			return false
		}
	case *ReleaseSavepointStmt:
		if !v.VisitReleaseSavepointStmt(n) {
			return false
		}
	case *RollbackToStmt:
		if !v.VisitRollbackToStmt(n) {
			return false
		}
	case *WithStmt:
		if !v.VisitWithStmt(n) {
			return false
		}
		for _, cte := range n.CTEs {
			if !AcceptStmt(cte.Query, v) {
				return false
			}
		}
		return AcceptStmt(n.Inner, v)
	case *TruncateStmt:
		if !v.VisitTruncateStmt(n) {
			return false
		}
	case *ReindexStmt:
		if !v.VisitReindexStmt(n) {
			return false
		}
	case *DropViewStmt:
		if !v.VisitDropViewStmt(n) {
			return false
		}
	case *DropTriggerStmt:
		if !v.VisitDropTriggerStmt(n) {
			return false
		}
	case *CreateMatViewStmt:
		if !v.VisitCreateMatViewStmt(n) {
			return false
		}
		return AcceptStmt(n.As, v)
	case *DropMatViewStmt:
		if !v.VisitDropMatViewStmt(n) {
			return false
		}
	case *RefreshMatViewStmt:
		if !v.VisitRefreshMatViewStmt(n) {
			return false
		}
	case *SetTransactionStmt:
		if !v.VisitSetTransactionStmt(n) {
			return false
		}
	case *ValuesStmt:
		if !v.VisitValuesStmt(n) {
			return false
		}
		for _, row := range n.Rows {
			for _, cell := range row {
				if !AcceptExpr(cell, v) {
					return false
				}
			}
		}
	case *AttachStmt:
		if !v.VisitAttachStmt(n) {
			return false
		}
		return AcceptExpr(n.Expr, v)
	case *DetachStmt:
		if !v.VisitDetachStmt(n) {
			return false
		}
	}
	return true
}
