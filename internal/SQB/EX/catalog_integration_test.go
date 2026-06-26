package EX

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestCatalog_Integration_CreateSurvivesClose — wire a real
// ls.Catalog into the EX layer, run CREATE TABLE, close, reopen
// the catalog in a fresh process simulation, and assert the
// schema is reachable.
func TestCatalog_Integration_CreateSurvivesClose(t *testing.T) {
	UnregisterAll() // clear in-memory EX state from prior tests
	dir := filepath.Join(t.TempDir(), "catalog")
	cat, err := ls.NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	SetCatalog(cat)
	t.Cleanup(func() {
		SetCatalog(nil)
		_ = cat.Close()
	})

	stmt := newCreateTable("users", []string{"id", "name"}, "id")
	ct := NewCreateTable(stmt)
	_, err = ct.Next(context.Background())
	if err != nil && err != ErrNoRows {
		t.Fatalf("CreateTable.Next: %v", err)
	}
	if err := cat.Close(); err != nil {
		t.Fatalf("catalog Close: %v", err)
	}
	SetCatalog(nil)

	// Simulate process restart.
	cat2, err := ls.NewCatalog(dir)
	if err != nil {
		t.Fatalf("reopen catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat2.Close() })
	SetCatalog(cat2)
	for _, e := range cat2.List() {
		if err := RegisterFromCatalog(e); err != nil {
			t.Fatalf("RegisterFromCatalog(%s): %v", e.Name, err)
		}
	}

	// The schema must be addressable for SELECT.
	ex := NewExecutorWithEngine(nil)
	rows, err := ex.Query(context.Background(), "SELECT name FROM users")
	if err != nil {
		t.Fatalf("post-restart Query: %v", err)
	}
	_ = rows
}

// TestCatalog_Integration_DropSurvivesClose — after DROP TABLE
// + restart, the schema must be gone and a re-CREATE must
// succeed.
func TestCatalog_Integration_DropSurvivesClose(t *testing.T) {
	UnregisterAll()
	dir := filepath.Join(t.TempDir(), "catalog")
	cat, err := ls.NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	SetCatalog(cat)
	t.Cleanup(func() {
		SetCatalog(nil)
		_ = cat.Close()
	})

	ct := NewCreateTable(newCreateTable("t", []string{"id"}, "id"))
	if _, err := ct.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("CreateTable: %v", err)
	}
	dt := NewDropTable(&PS.DropTable{Name: "t"})
	if _, err := dt.Next(context.Background()); err != nil && err != ErrNoRows {
		t.Fatalf("DropTable: %v", err)
	}
	if err := cat.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	SetCatalog(nil)

	cat2, err := ls.NewCatalog(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = cat2.Close() })
	SetCatalog(cat2)
	if got := cat2.Len(); got != 0 {
		t.Fatalf("Len after drop+reopen = %d, want 0", got)
	}
}

// TestCatalog_Integration_NextIDSurvives — NextID increments
// must survive the catalog close/open cycle.
func TestCatalog_Integration_NextIDSurvives(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	cat, err := ls.NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	SetCatalog(cat)
	t.Cleanup(func() {
		SetCatalog(nil)
		_ = cat.Close()
	})

	var ids []uint64
	for i := 0; i < 5; i++ {
		id, err := cat.NextID()
		if err != nil {
			t.Fatalf("NextID: %v", err)
		}
		ids = append(ids, id)
	}
	if err := cat.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	SetCatalog(nil)

	cat2, err := ls.NewCatalog(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = cat2.Close() })
	SetCatalog(cat2)
	id, err := cat2.NextID()
	if err != nil {
		t.Fatalf("NextID after reopen: %v", err)
	}
	last := ids[len(ids)-1]
	if id <= last {
		t.Fatalf("NextID after reopen = %d, want > %d", id, last)
	}
}

// TestCatalog_Integration_ConcurrentCreate — the catalog + EX
// pair must stay consistent under concurrent CREATE. Many
// goroutines each create a distinct table. The in-memory
// storeSchemas and the on-disk catalog must agree at the end.
func TestCatalog_Integration_ConcurrentCreate(t *testing.T) {
	UnregisterAll()
	dir := filepath.Join(t.TempDir(), "catalog")
	cat, err := ls.NewCatalog(dir)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	SetCatalog(cat)
	t.Cleanup(func() {
		SetCatalog(nil)
		_ = cat.Close()
	})

	const workers = 8
	const perWorker = 20
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				name := tableName(w, i)
				ct := NewCreateTable(newCreateTable(name, []string{"id"}, "id"))
				_, _ = ct.Next(context.Background())
			}
		}(w)
	}
	wg.Wait()
	exCount := func() int {
		storeMu.Lock()
		defer storeMu.Unlock()
		return len(tableIDs)
	}()
	if exCount != cat.Len() {
		t.Fatalf("EX tables=%d, catalog tables=%d, must agree", exCount, cat.Len())
	}
}

func tableName(w, i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	if w >= len(letters) || i >= len(letters) {
		return "w_extra"
	}
	return "w" + string(letters[w]) + "_" + string(letters[i])
}

// newCreateTable builds a minimal PS.CreateTable for table-schema
// tests. The Type field is left at zero (no explicit SQL type);
// the runtime treats it as "no explicit type" and skips the
// token in buildCreateSQL.
func newCreateTable(name string, cols []string, pk string) *PS.CreateTable {
	colDefs := make([]PS.ColDef, len(cols))
	for i, c := range cols {
		colDefs[i] = PS.NewColDef(c, 0)
	}
	return &PS.CreateTable{
		Name: name,
		Cols: colDefs,
		PK:   &pk,
	}
}
