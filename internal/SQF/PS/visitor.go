package PS

// Visitor defines the interface for AST traversal (REQ000583).
// Each Visit method is called for the corresponding AST node type.
// Return true to continue traversal into child nodes, false to stop.
type Visitor interface {
	// Expr visitors
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

	// Stmt visitors
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

// BaseVisitor provides default no-op implementations for all
// Visitor methods. Embed this in concrete visitors to avoid
// implementing every method (REQ000583).
type BaseVisitor struct{}

func (v *BaseVisitor) VisitNumberLiteral(*NumberLiteral) bool               { return true }
func (v *BaseVisitor) VisitFloatLiteral(*FloatLiteral) bool                 { return true }
func (v *BaseVisitor) VisitStringLiteral(*StringLiteral) bool               { return true }
func (v *BaseVisitor) VisitBoolLiteral(*BoolLiteral) bool                   { return true }
func (v *BaseVisitor) VisitNullLiteral(*NullLiteral) bool                   { return true }
func (v *BaseVisitor) VisitIdent(*Ident) bool                               { return true }
func (v *BaseVisitor) VisitQualifiedName(*QualifiedName) bool               { return true }
func (v *BaseVisitor) VisitAliasedExpr(*AliasedExpr) bool                   { return true }
func (v *BaseVisitor) VisitCastExpr(*CastExpr) bool                         { return true }
func (v *BaseVisitor) VisitParam(*Param) bool                               { return true }
func (v *BaseVisitor) VisitBinaryExpr(*BinaryExpr) bool                     { return true }
func (v *BaseVisitor) VisitUnaryExpr(*UnaryExpr) bool                       { return true }
func (v *BaseVisitor) VisitFunctionCall(*FunctionCall) bool                 { return true }
func (v *BaseVisitor) VisitAggregateFunc(*AggregateFunc) bool               { return true }
func (v *BaseVisitor) VisitWindowFunc(*WindowFunc) bool                     { return true }
func (v *BaseVisitor) VisitStarExpr(*StarExpr) bool                         { return true }
func (v *BaseVisitor) VisitListExpr(*ListExpr) bool                         { return true }
func (v *BaseVisitor) VisitBetweenExpr(*BetweenExpr) bool                   { return true }
func (v *BaseVisitor) VisitCaseExpr(*CaseExpr) bool                         { return true }
func (v *BaseVisitor) VisitInExpr(*InExpr) bool                             { return true }
func (v *BaseVisitor) VisitExistsExpr(*ExistsExpr) bool                     { return true }
func (v *BaseVisitor) VisitSubqueryExpr(*SubqueryExpr) bool                 { return true }
func (v *BaseVisitor) VisitIntervalLiteral(*IntervalLiteral) bool           { return true }
func (v *BaseVisitor) VisitRaiseFunc(*RaiseFunc) bool                       { return true }
func (v *BaseVisitor) VisitCreateTable(*CreateTable) bool                   { return true }
func (v *BaseVisitor) VisitDropTable(*DropTable) bool                       { return true }
func (v *BaseVisitor) VisitInsert(*Insert) bool                             { return true }
func (v *BaseVisitor) VisitUpdate(*Update) bool                             { return true }
func (v *BaseVisitor) VisitDelete(*Delete) bool                             { return true }
func (v *BaseVisitor) VisitSelect(*Select) bool                             { return true }
func (v *BaseVisitor) VisitCompoundStmt(*CompoundStmt) bool                 { return true }
func (v *BaseVisitor) VisitBeginTX(*BeginTX) bool                           { return true }
func (v *BaseVisitor) VisitCommitTX(*CommitTX) bool                         { return true }
func (v *BaseVisitor) VisitRollbackTX(*RollbackTX) bool                     { return true }
func (v *BaseVisitor) VisitExplainStmt(*ExplainStmt) bool                   { return true }
func (v *BaseVisitor) VisitCreateIndexStmt(*CreateIndexStmt) bool           { return true }
func (v *BaseVisitor) VisitDropIndexStmt(*DropIndexStmt) bool               { return true }
func (v *BaseVisitor) VisitCreateViewStmt(*CreateViewStmt) bool             { return true }
func (v *BaseVisitor) VisitAlterTableStmt(*AlterTableStmt) bool             { return true }
func (v *BaseVisitor) VisitPragmaStmt(*PragmaStmt) bool                     { return true }
func (v *BaseVisitor) VisitAnalyzeStmt(*AnalyzeStmt) bool                   { return true }
func (v *BaseVisitor) VisitVacuumStmt(*VacuumStmt) bool                     { return true }
func (v *BaseVisitor) VisitTriggerStmt(*TriggerStmt) bool                   { return true }
func (v *BaseVisitor) VisitSavepointStmt(*SavepointStmt) bool               { return true }
func (v *BaseVisitor) VisitReleaseSavepointStmt(*ReleaseSavepointStmt) bool { return true }
func (v *BaseVisitor) VisitRollbackToStmt(*RollbackToStmt) bool             { return true }
func (v *BaseVisitor) VisitWithStmt(*WithStmt) bool                         { return true }
func (v *BaseVisitor) VisitTruncateStmt(*TruncateStmt) bool                 { return true }
func (v *BaseVisitor) VisitReindexStmt(*ReindexStmt) bool                   { return true }
func (v *BaseVisitor) VisitDropViewStmt(*DropViewStmt) bool                 { return true }
func (v *BaseVisitor) VisitDropTriggerStmt(*DropTriggerStmt) bool           { return true }
func (v *BaseVisitor) VisitCreateMatViewStmt(*CreateMatViewStmt) bool       { return true }
func (v *BaseVisitor) VisitDropMatViewStmt(*DropMatViewStmt) bool           { return true }
func (v *BaseVisitor) VisitRefreshMatViewStmt(*RefreshMatViewStmt) bool     { return true }
func (v *BaseVisitor) VisitSetTransactionStmt(*SetTransactionStmt) bool     { return true }
func (v *BaseVisitor) VisitValuesStmt(*ValuesStmt) bool                     { return true }
func (v *BaseVisitor) VisitAttachStmt(*AttachStmt) bool                     { return true }
func (v *BaseVisitor) VisitDetachStmt(*DetachStmt) bool                     { return true }

// AcceptExpr dispatches to the appropriate Visitor method based on
// the concrete type of the expression (REQ000583).
func AcceptExpr(e Expr, v Visitor) bool {
	if e == nil {
		return true
	}
	switch n := e.(type) {
	case *NumberLiteral:
		return v.VisitNumberLiteral(n)
	case *FloatLiteral:
		return v.VisitFloatLiteral(n)
	case *StringLiteral:
		return v.VisitStringLiteral(n)
	case *BoolLiteral:
		return v.VisitBoolLiteral(n)
	case *NullLiteral:
		return v.VisitNullLiteral(n)
	case *Ident:
		return v.VisitIdent(n)
	case *QualifiedName:
		return v.VisitQualifiedName(n)
	case *AliasedExpr:
		return v.VisitAliasedExpr(n)
	case *CastExpr:
		return v.VisitCastExpr(n)
	case *Param:
		return v.VisitParam(n)
	case *BinaryExpr:
		return v.VisitBinaryExpr(n)
	case *UnaryExpr:
		return v.VisitUnaryExpr(n)
	case *FunctionCall:
		return v.VisitFunctionCall(n)
	case *AggregateFunc:
		return v.VisitAggregateFunc(n)
	case *WindowFunc:
		return v.VisitWindowFunc(n)
	case *StarExpr:
		return v.VisitStarExpr(n)
	case *ListExpr:
		return v.VisitListExpr(n)
	case *BetweenExpr:
		return v.VisitBetweenExpr(n)
	case *CaseExpr:
		return v.VisitCaseExpr(n)
	case *InExpr:
		return v.VisitInExpr(n)
	case *ExistsExpr:
		return v.VisitExistsExpr(n)
	case *SubqueryExpr:
		return v.VisitSubqueryExpr(n)
	case *IntervalLiteral:
		return v.VisitIntervalLiteral(n)
	case *RaiseFunc:
		return v.VisitRaiseFunc(n)
	default:
		return true
	}
}

// AcceptStmt dispatches to the appropriate Visitor method based on
// the concrete type of the statement (REQ000583).
func AcceptStmt(s Stmt, v Visitor) bool {
	if s == nil {
		return true
	}
	switch n := s.(type) {
	case *CreateTable:
		return v.VisitCreateTable(n)
	case *DropTable:
		return v.VisitDropTable(n)
	case *Insert:
		return v.VisitInsert(n)
	case *Update:
		return v.VisitUpdate(n)
	case *Delete:
		return v.VisitDelete(n)
	case *Select:
		return v.VisitSelect(n)
	case *CompoundStmt:
		return v.VisitCompoundStmt(n)
	case *BeginTX:
		return v.VisitBeginTX(n)
	case *CommitTX:
		return v.VisitCommitTX(n)
	case *RollbackTX:
		return v.VisitRollbackTX(n)
	case *ExplainStmt:
		return v.VisitExplainStmt(n)
	case *CreateIndexStmt:
		return v.VisitCreateIndexStmt(n)
	case *DropIndexStmt:
		return v.VisitDropIndexStmt(n)
	case *CreateViewStmt:
		return v.VisitCreateViewStmt(n)
	case *AlterTableStmt:
		return v.VisitAlterTableStmt(n)
	case *PragmaStmt:
		return v.VisitPragmaStmt(n)
	case *AnalyzeStmt:
		return v.VisitAnalyzeStmt(n)
	case *VacuumStmt:
		return v.VisitVacuumStmt(n)
	case *TriggerStmt:
		return v.VisitTriggerStmt(n)
	case *SavepointStmt:
		return v.VisitSavepointStmt(n)
	case *ReleaseSavepointStmt:
		return v.VisitReleaseSavepointStmt(n)
	case *RollbackToStmt:
		return v.VisitRollbackToStmt(n)
	case *WithStmt:
		return v.VisitWithStmt(n)
	case *TruncateStmt:
		return v.VisitTruncateStmt(n)
	case *ReindexStmt:
		return v.VisitReindexStmt(n)
	case *DropViewStmt:
		return v.VisitDropViewStmt(n)
	case *DropTriggerStmt:
		return v.VisitDropTriggerStmt(n)
	case *CreateMatViewStmt:
		return v.VisitCreateMatViewStmt(n)
	case *DropMatViewStmt:
		return v.VisitDropMatViewStmt(n)
	case *RefreshMatViewStmt:
		return v.VisitRefreshMatViewStmt(n)
	case *SetTransactionStmt:
		return v.VisitSetTransactionStmt(n)
	case *ValuesStmt:
		return v.VisitValuesStmt(n)
	case *AttachStmt:
		return v.VisitAttachStmt(n)
	case *DetachStmt:
		return v.VisitDetachStmt(n)
	default:
		return true
	}
}
