package EX

import (
	"context"
	"strconv"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

func TestIndexHint_ForcesIndex(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("users", []string{"id", "email", "name"}, "id")
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "CREATE INDEX idx_email ON users (email)"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		stmt := "INSERT INTO users VALUES (" + strconv.FormatInt(int64(i), 10) + ", 'user" + strconv.FormatInt(int64(i), 10) + "@x.com', 'U" + strconv.FormatInt(int64(i), 10) + "')"
		if _, err := ex.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT id FROM users INDEXED BY idx_email WHERE email = 'user2@x.com'")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Data[0].I64 != 2 {
		t.Fatalf("expected id=2, got %v", rows)
	}
}

func TestIndexHint_InvalidName_Error(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("users", []string{"id"}, "id")
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "INSERT INTO users VALUES (1)"); err != nil {
		t.Fatal(err)
	}

	_, err := ex.QueryAll(ctx, "SELECT id FROM users INDEXED BY nonexistent WHERE id = 1")
	if err == nil {
		t.Fatal("expected error for nonexistent index hint")
	}
}

func TestIndexHint_NotIndexed_ForcesSeqScan(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("users", []string{"id", "email"}, "id")
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "CREATE INDEX idx_email ON users (email)"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		stmt := "INSERT INTO users VALUES (" + strconv.FormatInt(int64(i), 10) + ", 'user" + strconv.FormatInt(int64(i), 10) + "@x.com')"
		if _, err := ex.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	// NOT INDEXED forces a sequential scan even though an index on email exists.
	rows, err := ex.QueryAll(ctx, "SELECT email FROM users NOT INDEXED WHERE email = 'user2@x.com'")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Data[0].S != "user2@x.com" {
		t.Fatalf("expected 'user2@x.com', got %v", rows)
	}
}

func TestIndexHint_WithoutIndex_Fallback(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("users", []string{"id"}, "id")
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		if _, err := ex.Exec(ctx, "INSERT INTO users VALUES ("+strconv.FormatInt(int64(i), 10)+")"); err != nil {
			t.Fatal(err)
		}
	}

	// Without any index hint, query should still work (fallback to SeqScan).
	rows, err := ex.QueryAll(ctx, "SELECT id FROM users WHERE id = 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Data[0].I64 != 2 {
		t.Fatalf("expected id=2, got %v", rows)
	}
}
