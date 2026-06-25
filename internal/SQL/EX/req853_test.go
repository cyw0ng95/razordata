package EX

import (
	"context"
	"testing"
)

func TestReq853_CHANGES(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ctx := context.Background()

	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "val"})

	ex.Exec(ctx, "INSERT INTO t VALUES (1, 'a')")
	rs, _ := ex.QueryAll(ctx, "SELECT CHANGES()")
	if rs[0].Data[0].ToAny() != int64(1) {
		t.Errorf("after INSERT CHANGES()=%v want 1", rs[0].Data[0].ToAny())
	}
	rs, _ = ex.QueryAll(ctx, "SELECT TOTAL_CHANGES()")
	if rs[0].Data[0].ToAny() != int64(1) {
		t.Errorf("TOTAL_CHANGES=%v want 1", rs[0].Data[0].ToAny())
	}

	ex.Exec(ctx, "UPDATE t SET val = 'b' WHERE id = 1")
	ex.Exec(ctx, "DELETE FROM t WHERE id = 1")
	rs, _ = ex.QueryAll(ctx, "SELECT TOTAL_CHANGES()")
	if rs[0].Data[0].ToAny() != int64(3) {
		t.Errorf("TOTAL_CHANGES after 3 DML=%v want 3", rs[0].Data[0].ToAny())
	}
}
