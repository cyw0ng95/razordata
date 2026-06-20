package PS

import (
	"testing"
)

// testVisitor is a minimal visitor that tracks which methods were called.
type testVisitor struct {
	BaseVisitor
	called map[string]bool
}

func newTestVisitor() *testVisitor {
	return &testVisitor{called: make(map[string]bool)}
}

func (v *testVisitor) VisitNumberLiteral(*NumberLiteral) bool       { v.called["NumberLiteral"] = true; return true }
func (v *testVisitor) VisitFloatLiteral(*FloatLiteral) bool         { v.called["FloatLiteral"] = true; return true }
func (v *testVisitor) VisitStringLiteral(*StringLiteral) bool       { v.called["StringLiteral"] = true; return true }
func (v *testVisitor) VisitBoolLiteral(*BoolLiteral) bool           { v.called["BoolLiteral"] = true; return true }
func (v *testVisitor) VisitNullLiteral(*NullLiteral) bool           { v.called["NullLiteral"] = true; return true }
func (v *testVisitor) VisitIdent(*Ident) bool                       { v.called["Ident"] = true; return true }
func (v *testVisitor) VisitQualifiedName(*QualifiedName) bool       { v.called["QualifiedName"] = true; return true }
func (v *testVisitor) VisitAliasedExpr(*AliasedExpr) bool           { v.called["AliasedExpr"] = true; return true }
func (v *testVisitor) VisitCastExpr(*CastExpr) bool                 { v.called["CastExpr"] = true; return true }
func (v *testVisitor) VisitParam(*Param) bool                       { v.called["Param"] = true; return true }
func (v *testVisitor) VisitBinaryExpr(*BinaryExpr) bool             { v.called["BinaryExpr"] = true; return true }
func (v *testVisitor) VisitUnaryExpr(*UnaryExpr) bool               { v.called["UnaryExpr"] = true; return true }
func (v *testVisitor) VisitFunctionCall(*FunctionCall) bool         { v.called["FunctionCall"] = true; return true }
func (v *testVisitor) VisitAggregateFunc(*AggregateFunc) bool       { v.called["AggregateFunc"] = true; return true }
func (v *testVisitor) VisitWindowFunc(*WindowFunc) bool             { v.called["WindowFunc"] = true; return true }
func (v *testVisitor) VisitStarExpr(*StarExpr) bool                 { v.called["StarExpr"] = true; return true }
func (v *testVisitor) VisitListExpr(*ListExpr) bool                 { v.called["ListExpr"] = true; return true }
func (v *testVisitor) VisitBetweenExpr(*BetweenExpr) bool           { v.called["BetweenExpr"] = true; return true }
func (v *testVisitor) VisitCaseExpr(*CaseExpr) bool                 { v.called["CaseExpr"] = true; return true }
func (v *testVisitor) VisitInExpr(*InExpr) bool                     { v.called["InExpr"] = true; return true }
func (v *testVisitor) VisitExistsExpr(*ExistsExpr) bool             { v.called["ExistsExpr"] = true; return true }
func (v *testVisitor) VisitSubqueryExpr(*SubqueryExpr) bool         { v.called["SubqueryExpr"] = true; return true }
func (v *testVisitor) VisitIntervalLiteral(*IntervalLiteral) bool   { v.called["IntervalLiteral"] = true; return true }
func (v *testVisitor) VisitRaiseFunc(*RaiseFunc) bool               { v.called["RaiseFunc"] = true; return true }
func (v *testVisitor) VisitCreateTable(*CreateTable) bool           { v.called["CreateTable"] = true; return true }
func (v *testVisitor) VisitDropTable(*DropTable) bool               { v.called["DropTable"] = true; return true }
func (v *testVisitor) VisitInsert(*Insert) bool                     { v.called["Insert"] = true; return true }
func (v *testVisitor) VisitUpdate(*Update) bool                     { v.called["Update"] = true; return true }
func (v *testVisitor) VisitDelete(*Delete) bool                     { v.called["Delete"] = true; return true }
func (v *testVisitor) VisitSelect(*Select) bool                     { v.called["Select"] = true; return true }
func (v *testVisitor) VisitCompoundStmt(*CompoundStmt) bool         { v.called["CompoundStmt"] = true; return true }
func (v *testVisitor) VisitBeginTX(*BeginTX) bool                   { v.called["BeginTX"] = true; return true }
func (v *testVisitor) VisitCommitTX(*CommitTX) bool                 { v.called["CommitTX"] = true; return true }
func (v *testVisitor) VisitRollbackTX(*RollbackTX) bool             { v.called["RollbackTX"] = true; return true }
func (v *testVisitor) VisitExplainStmt(*ExplainStmt) bool           { v.called["ExplainStmt"] = true; return true }
func (v *testVisitor) VisitCreateIndexStmt(*CreateIndexStmt) bool   { v.called["CreateIndexStmt"] = true; return true }
func (v *testVisitor) VisitDropIndexStmt(*DropIndexStmt) bool       { v.called["DropIndexStmt"] = true; return true }
func (v *testVisitor) VisitCreateViewStmt(*CreateViewStmt) bool     { v.called["CreateViewStmt"] = true; return true }
func (v *testVisitor) VisitAlterTableStmt(*AlterTableStmt) bool     { v.called["AlterTableStmt"] = true; return true }
func (v *testVisitor) VisitPragmaStmt(*PragmaStmt) bool             { v.called["PragmaStmt"] = true; return true }
func (v *testVisitor) VisitAnalyzeStmt(*AnalyzeStmt) bool           { v.called["AnalyzeStmt"] = true; return true }
func (v *testVisitor) VisitVacuumStmt(*VacuumStmt) bool             { v.called["VacuumStmt"] = true; return true }
func (v *testVisitor) VisitTriggerStmt(*TriggerStmt) bool           { v.called["TriggerStmt"] = true; return true }
func (v *testVisitor) VisitSavepointStmt(*SavepointStmt) bool       { v.called["SavepointStmt"] = true; return true }
func (v *testVisitor) VisitReleaseSavepointStmt(*ReleaseSavepointStmt) bool { v.called["ReleaseSavepointStmt"] = true; return true }
func (v *testVisitor) VisitRollbackToStmt(*RollbackToStmt) bool     { v.called["RollbackToStmt"] = true; return true }
func (v *testVisitor) VisitWithStmt(*WithStmt) bool                 { v.called["WithStmt"] = true; return true }
func (v *testVisitor) VisitTruncateStmt(*TruncateStmt) bool         { v.called["TruncateStmt"] = true; return true }
func (v *testVisitor) VisitReindexStmt(*ReindexStmt) bool           { v.called["ReindexStmt"] = true; return true }
func (v *testVisitor) VisitDropViewStmt(*DropViewStmt) bool         { v.called["DropViewStmt"] = true; return true }
func (v *testVisitor) VisitDropTriggerStmt(*DropTriggerStmt) bool   { v.called["DropTriggerStmt"] = true; return true }
func (v *testVisitor) VisitCreateMatViewStmt(*CreateMatViewStmt) bool { v.called["CreateMatViewStmt"] = true; return true }
func (v *testVisitor) VisitDropMatViewStmt(*DropMatViewStmt) bool   { v.called["DropMatViewStmt"] = true; return true }
func (v *testVisitor) VisitRefreshMatViewStmt(*RefreshMatViewStmt) bool { v.called["RefreshMatViewStmt"] = true; return true }
func (v *testVisitor) VisitSetTransactionStmt(*SetTransactionStmt) bool { v.called["SetTransactionStmt"] = true; return true }
func (v *testVisitor) VisitValuesStmt(*ValuesStmt) bool             { v.called["ValuesStmt"] = true; return true }
func (v *testVisitor) VisitAttachStmt(*AttachStmt) bool             { v.called["AttachStmt"] = true; return true }
func (v *testVisitor) VisitDetachStmt(*DetachStmt) bool             { v.called["DetachStmt"] = true; return true }

func TestAcceptExpr_AllTypes(t *testing.T) {
	exprs := []struct {
		name string
		expr Expr
	}{
		{"NumberLiteral", &NumberLiteral{Val: 1}},
		{"FloatLiteral", &FloatLiteral{Val: 1.0}},
		{"StringLiteral", &StringLiteral{Val: "test"}},
		{"BoolLiteral", &BoolLiteral{Val: true}},
		{"NullLiteral", &NullLiteral{}},
		{"Ident", &Ident{Name: "x"}},
		{"QualifiedName", &QualifiedName{Table: "t", Name: "c"}},
		{"AliasedExpr", &AliasedExpr{Expr: &Ident{Name: "x"}, Alias: "y"}},
		{"CastExpr", &CastExpr{Expr: &Ident{Name: "x"}, Type: nil}},
		{"Param", &Param{Index: 0}},
		{"BinaryExpr", &BinaryExpr{Op: 0, Left: &Ident{Name: "x"}, Right: &NumberLiteral{Val: 1}}},
		{"UnaryExpr", &UnaryExpr{Op: 0, Operand: &Ident{Name: "x"}}},
		{"FunctionCall", &FunctionCall{Name: "f", Args: nil}},
		{"AggregateFunc", &AggregateFunc{Name: "count", Arg: &StarExpr{}}},
		{"WindowFunc", &WindowFunc{Name: "row_number"}},
		{"StarExpr", &StarExpr{}},
		{"ListExpr", &ListExpr{Items: []Expr{&NumberLiteral{Val: 1}}}},
		{"BetweenExpr", &BetweenExpr{Expr: &Ident{Name: "x"}, Low: &NumberLiteral{Val: 1}, High: &NumberLiteral{Val: 10}}},
		{"CaseExpr", &CaseExpr{Expr: nil, WhenList: nil, Else: nil}},
		{"InExpr", &InExpr{Expr: &Ident{Name: "x"}, List: nil, Subquery: nil}},
		{"ExistsExpr", &ExistsExpr{Subquery: &Select{}}},
		{"SubqueryExpr", &SubqueryExpr{Subquery: &Select{}}},
		{"IntervalLiteral", &IntervalLiteral{Value: "7", Unit: "DAY"}},
		{"RaiseFunc", &RaiseFunc{Action: "ABORT", Message: &StringLiteral{Val: "err"}}},
	}

	for _, tc := range exprs {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestVisitor()
			AcceptExpr(tc.expr, v)
			if !v.called[tc.name] {
				t.Errorf("Visit%s not called", tc.name)
			}
		})
	}
}

func TestAcceptStmt_AllTypes(t *testing.T) {
	stmts := []struct {
		name string
		stmt Stmt
	}{
		{"CreateTable", &CreateTable{Name: "t"}},
		{"DropTable", &DropTable{Name: "t"}},
		{"Insert", &Insert{Table: "t"}},
		{"Update", &Update{Table: "t"}},
		{"Delete", &Delete{Table: "t"}},
		{"Select", &Select{From: "t"}},
		{"CompoundStmt", &CompoundStmt{Left: &Select{}, Op: CompoundUnion, Right: &Select{}}},
		{"BeginTX", &BeginTX{}},
		{"CommitTX", &CommitTX{}},
		{"RollbackTX", &RollbackTX{}},
		{"ExplainStmt", &ExplainStmt{Inner: &Select{}}},
		{"CreateIndexStmt", &CreateIndexStmt{Name: "idx", Table: "t"}},
		{"DropIndexStmt", &DropIndexStmt{Name: "idx"}},
		{"CreateViewStmt", &CreateViewStmt{Name: "v", As: &Select{}}},
		{"AlterTableStmt", &AlterTableStmt{Table: "t", Action: "ADD COLUMN"}},
		{"PragmaStmt", &PragmaStmt{Name: "journal_mode"}},
		{"AnalyzeStmt", &AnalyzeStmt{}},
		{"VacuumStmt", &VacuumStmt{}},
		{"TriggerStmt", &TriggerStmt{Name: "trig"}},
		{"SavepointStmt", &SavepointStmt{Name: "sp1"}},
		{"ReleaseSavepointStmt", &ReleaseSavepointStmt{Name: "sp1"}},
		{"RollbackToStmt", &RollbackToStmt{Name: "sp1"}},
		{"WithStmt", &WithStmt{Inner: &Select{}}},
		{"TruncateStmt", &TruncateStmt{Table: "t"}},
		{"ReindexStmt", &ReindexStmt{}},
		{"DropViewStmt", &DropViewStmt{Name: "v"}},
		{"DropTriggerStmt", &DropTriggerStmt{Name: "trig"}},
		{"CreateMatViewStmt", &CreateMatViewStmt{Name: "mv", As: &Select{}}},
		{"DropMatViewStmt", &DropMatViewStmt{Name: "mv"}},
		{"RefreshMatViewStmt", &RefreshMatViewStmt{Name: "mv"}},
		{"SetTransactionStmt", &SetTransactionStmt{Level: "READ COMMITTED"}},
		{"ValuesStmt", &ValuesStmt{Rows: [][]Expr{{&NumberLiteral{Val: 1}}}}},
		{"AttachStmt", &AttachStmt{Expr: &StringLiteral{Val: "path"}, Name: "s"}},
		{"DetachStmt", &DetachStmt{Name: "s"}},
	}

	for _, tc := range stmts {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestVisitor()
			AcceptStmt(tc.stmt, v)
			if !v.called[tc.name] {
				t.Errorf("Visit%s not called", tc.name)
			}
		})
	}
}

func TestAcceptExpr_Nil(t *testing.T) {
	v := newTestVisitor()
	if !AcceptExpr(nil, v) {
		t.Error("AcceptExpr(nil) should return true")
	}
}

func TestAcceptStmt_Nil(t *testing.T) {
	v := newTestVisitor()
	if !AcceptStmt(nil, v) {
		t.Error("AcceptStmt(nil) should return true")
	}
}

func TestBaseVisitor_DefaultBehavior(t *testing.T) {
	v := &BaseVisitor{}
	if !v.VisitNumberLiteral(nil) {
		t.Error("BaseVisitor.VisitNumberLiteral should return true")
	}
	if !v.VisitSelect(nil) {
		t.Error("BaseVisitor.VisitSelect should return true")
	}
}
