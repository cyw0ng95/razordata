package EX

import (
	"context"
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// TestCreateTriggerRegression verifies REQ000435: CREATE TRIGGER
// parsing. The body executes as a no-op stub; future iterations wire
// the trigger to INSERT/UPDATE/DELETE writers.
func TestCreateTriggerRegression(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"a", "b"})
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO t1 VALUES (1, 2)"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	probes := []string{
		"CREATE TRIGGER t AFTER INSERT ON t1 BEGIN SELECT 1; END",
		"CREATE TRIGGER t2 BEFORE DELETE ON t1 BEGIN UPDATE t1 SET a = a + 1; END",
		"CREATE TRIGGER t3 AFTER UPDATE OF a ON t1 BEGIN SELECT 1; END",
		"CREATE TRIGGER IF NOT EXISTS t4 AFTER INSERT ON t1 BEGIN SELECT 1; END",
	}
	for _, sql := range probes {
		_, err := ex.Exec(ctx, sql)
		if err != nil {
			t.Errorf("trigger %q failed: %v", sql, err)
		}
	}

	// Verify AST shape
	parser := PS.NewParser("CREATE TRIGGER t5 BEFORE INSERT ON t1 FOR EACH ROW BEGIN SELECT 1; END")
	stmt, err := parser.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	trig, ok := stmt.(*PS.TriggerStmt)
	if !ok {
		t.Fatalf("expected *TriggerStmt, got %T", stmt)
	}
	if trig.Name != "t5" {
		t.Errorf("name: %q", trig.Name)
	}
	if !strings.EqualFold(trig.Time, "BEFORE") {
		t.Errorf("time: %q", trig.Time)
	}
	if !strings.EqualFold(trig.Event, "INSERT") {
		t.Errorf("event: %q", trig.Event)
	}
	if trig.OnTable != "t1" {
		t.Errorf("table: %q", trig.OnTable)
	}
	if len(trig.Body) == 0 {
		t.Errorf("body empty")
	}
}
