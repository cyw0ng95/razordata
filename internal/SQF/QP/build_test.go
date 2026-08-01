package QP

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// parseOne parses a single SQL statement, closing the pooled parser. It
// fails the test on parse error.
func parseOne(t *testing.T, sql string) PS.Stmt {
	t.Helper()
	p := PS.NewParser(sql)
	defer p.Close()
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("parse %q: %v", sql, err)
	}
	return stmt
}

func TestBuildQueryPlan_Select(t *testing.T) {
	stmt := parseOne(t, "SELECT a, b FROM t WHERE a > 1")
	plan, err := BuildQueryPlan(stmt)
	if err != nil {
		t.Fatalf("BuildQueryPlan: %v", err)
	}
	root := plan.Root
	if root.Op != OpProject {
		t.Fatalf("root op = %s, want Project", root.Op)
	}
	if len(root.Exprs) != 2 {
		t.Fatalf("project exprs = %d, want 2", len(root.Exprs))
	}
	if len(root.Children) != 1 {
		t.Fatalf("project children = %d, want 1", len(root.Children))
	}
	filt := root.Children[0]
	if filt.Op != OpFilter {
		t.Fatalf("child op = %s, want Filter", filt.Op)
	}
	if filt.Pred == nil {
		t.Fatal("filter predicate is nil")
	}
	scan := filt.Children[0]
	if scan.Op != OpSeqScan {
		t.Fatalf("scan op = %s, want SeqScan", scan.Op)
	}
	if scan.Table != "t" {
		t.Fatalf("scan table = %q, want t", scan.Table)
	}
}

func TestBuildQueryPlan_InsertValues(t *testing.T) {
	stmt := parseOne(t, "INSERT INTO t(a, b) VALUES (1, 2)")
	plan, err := BuildQueryPlan(stmt)
	if err != nil {
		t.Fatalf("BuildQueryPlan: %v", err)
	}
	root := plan.Root
	if root.Op != OpInsert {
		t.Fatalf("root op = %s, want Insert", root.Op)
	}
	if root.Table != "t" {
		t.Fatalf("table = %q, want t", root.Table)
	}
	if len(root.Cols) != 2 || root.Cols[0] != "a" || root.Cols[1] != "b" {
		t.Fatalf("cols = %v, want [a b]", root.Cols)
	}
	if len(root.Values) != 1 || len(root.Values[0]) != 2 {
		t.Fatalf("values = %v, want 1 row of 2 exprs", root.Values)
	}
	// Literal INSERT has no child source plan.
	if len(root.Children) != 0 {
		t.Fatalf("insert children = %d, want 0 (literal values)", len(root.Children))
	}
}

func TestBuildQueryPlan_InsertSelect(t *testing.T) {
	stmt := parseOne(t, "INSERT INTO t(a, b) SELECT a, b FROM src WHERE a > 1")
	plan, err := BuildQueryPlan(stmt)
	if err != nil {
		t.Fatalf("BuildQueryPlan: %v", err)
	}
	root := plan.Root
	if root.Op != OpInsert {
		t.Fatalf("root op = %s, want Insert", root.Op)
	}
	if len(root.Children) != 1 {
		t.Fatalf("insert children = %d, want 1 (SELECT source)", len(root.Children))
	}
	if root.Children[0].Op != OpProject {
		t.Fatalf("source op = %s, want Project", root.Children[0].Op)
	}
}

func TestBuildQueryPlan_Update(t *testing.T) {
	stmt := parseOne(t, "UPDATE t SET a = 1 WHERE b > 2")
	plan, err := BuildQueryPlan(stmt)
	if err != nil {
		t.Fatalf("BuildQueryPlan: %v", err)
	}
	root := plan.Root
	if root.Op != OpUpdate {
		t.Fatalf("root op = %s, want Update", root.Op)
	}
	if root.Table != "t" {
		t.Fatalf("table = %q, want t", root.Table)
	}
	if len(root.Set) != 1 {
		t.Fatalf("set clauses = %d, want 1", len(root.Set))
	}
	if root.Where == nil {
		t.Fatal("update WHERE is nil")
	}
	if len(root.Children) != 1 {
		t.Fatalf("update children = %d, want 1", len(root.Children))
	}
	// WHERE is modeled as a pre-filter on the scan child (WT applies the
	// mutation to every iter row and does not evaluate where itself).
	child := root.Children[0]
	if child.Op != OpSeqScan && child.Op != OpFilter {
		t.Fatalf("update child op = %s, want SeqScan or Filter", child.Op)
	}
	if child.Op == OpFilter && (len(child.Children) != 1 || child.Children[0].Op != OpSeqScan) {
		t.Fatalf("update filter child = %v, want SeqScan", child.Children)
	}
}

func TestBuildQueryPlan_Delete(t *testing.T) {
	stmt := parseOne(t, "DELETE FROM t WHERE a = 1")
	plan, err := BuildQueryPlan(stmt)
	if err != nil {
		t.Fatalf("BuildQueryPlan: %v", err)
	}
	root := plan.Root
	if root.Op != OpDelete {
		t.Fatalf("root op = %s, want Delete", root.Op)
	}
	if root.Table != "t" {
		t.Fatalf("table = %q, want t", root.Table)
	}
	if root.Where == nil {
		t.Fatal("delete WHERE is nil")
	}
	if len(root.Children) != 1 {
		t.Fatalf("delete children = %d, want 1", len(root.Children))
	}
	child := root.Children[0]
	if child.Op != OpSeqScan && child.Op != OpFilter {
		t.Fatalf("delete child op = %s, want SeqScan or Filter", child.Op)
	}
	if child.Op == OpFilter && (len(child.Children) != 1 || child.Children[0].Op != OpSeqScan) {
		t.Fatalf("delete filter child = %v, want SeqScan", child.Children)
	}
}

func TestBuildQueryPlan_Distinct(t *testing.T) {
	stmt := parseOne(t, "SELECT DISTINCT a FROM t")
	plan, err := BuildQueryPlan(stmt)
	if err != nil {
		t.Fatalf("BuildQueryPlan: %v", err)
	}
	proj := plan.Root
	if proj.Op != OpProject || !proj.Distinct {
		t.Fatalf("root = %s distinct=%v, want Project distinct=true", proj.Op, proj.Distinct)
	}
}

func TestBuildQueryPlan_LimitOffset(t *testing.T) {
	stmt := parseOne(t, "SELECT a FROM t LIMIT 10 OFFSET 5")
	plan, err := BuildQueryPlan(stmt)
	if err != nil {
		t.Fatalf("BuildQueryPlan: %v", err)
	}
	if plan.Root.Op != OpLimit {
		t.Fatalf("root op = %s, want Limit", plan.Root.Op)
	}
	if plan.Root.LimitExpr == nil || plan.Root.OffsetExpr == nil {
		t.Fatal("limit/offset expressions missing")
	}
}

func TestBuildQueryPlan_Join(t *testing.T) {
	stmt := parseOne(t, "SELECT * FROM t1 JOIN t2 ON t1.id = t2.id")
	plan, err := BuildQueryPlan(stmt)
	if err != nil {
		t.Fatalf("BuildQueryPlan: %v", err)
	}
	proj := plan.Root
	if proj.Op != OpProject {
		t.Fatalf("root op = %s, want Project", proj.Op)
	}
	join := proj.Children[0]
	if join.Op != OpHashJoin {
		t.Fatalf("join op = %s, want HashJoin", join.Op)
	}
	if len(join.Children) != 2 {
		t.Fatalf("join children = %d, want 2", len(join.Children))
	}
	if join.Children[0].Table != "t1" || join.Children[1].Table != "t2" {
		t.Fatalf("join tables = %q,%q want t1,t2", join.Children[0].Table, join.Children[1].Table)
	}
	if join.On == nil {
		t.Fatal("join ON predicate is nil")
	}
}

func TestBuildQueryPlan_Unsupported(t *testing.T) {
	stmt := parseOne(t, "CREATE TABLE u(x INT)")
	if _, err := BuildQueryPlan(stmt); err == nil {
		t.Fatal("expected error for unsupported CREATE TABLE statement, got nil")
	}
}

// The following statements are *modeled* structurally by the builder but the
// execution path cannot lower them faithfully yet, so BuildQueryPlan returns
// an error and the caller falls back to the legacy operator tree.

func TestBuildQueryPlan_Fallback_AggregateSelect(t *testing.T) {
	stmt := parseOne(t, "SELECT count(*) FROM t")
	if _, err := BuildQueryPlan(stmt); err == nil {
		t.Fatal("expected error for aggregate SELECT (falls back to OP), got nil")
	}
}

func TestBuildQueryPlan_Fallback_SubquerySelect(t *testing.T) {
	stmt := parseOne(t, "SELECT a FROM t WHERE a IN (SELECT b FROM u)")
	if _, err := BuildQueryPlan(stmt); err == nil {
		t.Fatal("expected error for subquery SELECT (falls back to OP), got nil")
	}
}

func TestBuildQueryPlan_Fallback_UpdateWithLimit(t *testing.T) {
	stmt := parseOne(t, "UPDATE t SET a = 1 LIMIT 5")
	if _, err := BuildQueryPlan(stmt); err == nil {
		t.Fatal("expected error for UPDATE ... LIMIT (falls back to OP), got nil")
	}
}

func TestBuildQueryPlan_Fallback_InsertDefaultValues(t *testing.T) {
	stmt := parseOne(t, "INSERT INTO t DEFAULT VALUES")
	if _, err := BuildQueryPlan(stmt); err == nil {
		t.Fatal("expected error for INSERT DEFAULT VALUES (falls back to OP), got nil")
	}
}
