package EX

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestVacuum_Integration(t *testing.T) {
	// Parse VACUUM
	parser := PS.NewParser("VACUUM")
	stmt, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	vacuum, ok := stmt.(*PS.VacuumStmt)
	if !ok {
		t.Fatalf("expected *PS.VacuumStmt, got %T", stmt)
	}

	// Plan without store (should still succeed, just skip compaction)
	pl := NewPlanner()
	plan, err := pl.Plan(vacuum)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Execute
	ctx := context.Background()
	op := plan.Root
	defer op.Close()

	_, err = op.Next(ctx)
	if err != DT.ErrNoRows {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}

	// Vacuum without store still returns success
	_ = op
}

func TestVacuum_WithStore(t *testing.T) {
	// This test creates a real engine store, inserts some data,
	// deletes some (creating tombstones), then runs VACUUM
	dir := t.TempDir()
	eng, err := ls.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatalf("ls.Open: %v", err)
	}
	defer eng.Close()

	store := &engineStore{eng: eng}

	// Insert some keys
	for i := 0; i < 10; i++ {
		key := []byte(fmt.Sprintf("key%03d", i))
		val := []byte(fmt.Sprintf("value%03d", i))
		if err := store.Insert(key, val); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// Delete half (creates tombstones)
	for i := 0; i < 5; i++ {
		key := []byte(fmt.Sprintf("key%03d", i))
		if err := store.Delete(key); err != nil {
			t.Fatalf("Delete failed: %v", err)
		}
	}

	// Verify tombstones exist (get should fail for deleted keys)
	for i := 0; i < 5; i++ {
		key := []byte(fmt.Sprintf("key%03d", i))
		_, found, err := store.Get(key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if found {
			t.Errorf("key %q should be deleted but was found", key)
		}
	}

	// Run VACUUM (triggers compaction)
	vacuum := NewVacuumWithStore(&PS.VacuumStmt{}, store)
	defer vacuum.Close()

	ctx := context.Background()
	_, err = vacuum.Next(ctx)
	if err != DT.ErrNoRows {
		t.Fatalf("Vacuum Next failed: %v", err)
	}

	if vacuum.RowsAffected() != 1 {
		t.Errorf("RowsAffected: got %d, want 1", vacuum.RowsAffected())
	}
}
