package EX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

func TestCreateMatViewOperator(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	sel := &PS.Select{From: "t1", Cols: []PS.Expr{&PS.Ident{Name: "a"}}}
	op := NewCreateMatView("mv_test", sel, nil)

	ctx := context.Background()
	row, err := op.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(row.Data) == 0 || row.Data[0].ToAny().(string) != "materialized view created" {
		t.Errorf("unexpected result: %v", row.Data)
	}

	if LookupMatView("mv_test") == nil {
		t.Error("mat view should be registered")
	}

	_, err = op.Next(ctx)
	if err != ErrNoRows {
		t.Errorf("second Next should return ErrNoRows, got %v", err)
	}
}

func TestDropMatViewOperator(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	sel := &PS.Select{From: "t1", Cols: []PS.Expr{&PS.Ident{Name: "a"}}}
	RegisterMatView("mv_drop", sel)

	op := NewDropMatView("mv_drop", nil)
	ctx := context.Background()
	row, err := op.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(row.Data) == 0 || row.Data[0].ToAny().(string) != "materialized view dropped" {
		t.Errorf("unexpected result: %v", row.Data)
	}

	if LookupMatView("mv_drop") != nil {
		t.Error("mat view should be unregistered after drop")
	}
}

func TestDropMatView_NotFound(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	op := NewDropMatView("nonexistent", nil)
	_, err := op.Next(context.Background())
	if err == nil {
		t.Fatal("expected error for nonexistent mat view")
	}
}

func TestRefreshMatViewOperator(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	leftRows := []Row{
		{Cols: []string{"id"}, Data: []Value{NewIntValue(int64(1))}},
		{Cols: []string{"id"}, Data: []Value{NewIntValue(int64(2))}},
	}
	RegisterTable("t1", leftRows)

	sel := &PS.Select{From: "t1", Cols: []PS.Expr{&PS.Ident{Name: "id"}}}
	RegisterMatView("mv_ref", sel)

	planner := NewPlanner()
	planner.catalog["t1"] = &tableInfo{
		name: "t1",
		cols: []ColInfo{{Name: "id", Typ: 0}},
	}

	op := NewRefreshMatView("mv_ref", sel, nil, planner)
	ctx := context.Background()
	row, err := op.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(row.Data) == 0 {
		t.Fatal("empty result")
	}
	resultStr, ok := row.Data[0].ToAny().(string)
	if !ok {
		t.Fatalf("expected string, got %T", row.Data[0])
	}
	if resultStr != "refreshed, 2 rows" {
		t.Errorf("unexpected result: %s", resultStr)
	}
}

func TestRefreshMatView_NotFound(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	planner := NewPlanner()
	op := NewRefreshMatView("nonexistent", nil, nil, planner)
	_, err := op.Next(context.Background())
	if err == nil {
		t.Fatal("expected error for nonexistent mat view")
	}
}

func TestMatViewMetaKey(t *testing.T) {
	key := MatViewMetaKey("test")
	if string(key) != "_matview:test:meta" {
		t.Errorf("unexpected meta key: %s", key)
	}
}

func TestMatViewDataPrefix(t *testing.T) {
	prefix := MatViewDataPrefix("test")
	if string(prefix) != "_matview:test:data:" {
		t.Errorf("unexpected data prefix: %s", prefix)
	}
}

func TestEncodeMatViewRow(t *testing.T) {
	row := Row{
		Cols: []string{"id", "name", "score"},
		Data: []Value{NewIntValue(int64(42)), NewTextValue("hello"), NewFloatValue(float64(42))},
	}
	encoded := encodeMatViewRow(row)
	if len(encoded) == 0 {
		t.Error("encoded row should not be empty")
	}
}

func TestCreateMatView_WithStore(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	sel := &PS.Select{From: "t1", Cols: []PS.Expr{&PS.Ident{Name: "a"}}}
	store := newMemStore()
	op := NewCreateMatView("mv_store", sel, store)

	ctx := context.Background()
	_, err := op.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}

	metaKey := MatViewMetaKey("mv_store")
	val, found, err := store.Get(metaKey)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Error("meta key should exist in store")
	}
	if string(val) != "1" {
		t.Errorf("meta value should be '1', got '%s'", val)
	}

	if LookupMatView("mv_store") == nil {
		t.Error("mat view should be registered")
	}
}

func TestDropMatView_WithStore(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	store := newMemStore()
	RegisterMatView("mv_drop_store", &PS.Select{From: "t1"})
	_ = store.Insert(MatViewMetaKey("mv_drop_store"), []byte("1"))
	_ = store.Insert(MatViewDataPrefix("mv_drop_store"), []byte("data"))

	op := NewDropMatView("mv_drop_store", store)
	_, err := op.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}

	if LookupMatView("mv_drop_store") != nil {
		t.Error("mat view should be unregistered")
	}

	_, found, err := store.Get(MatViewMetaKey("mv_drop_store"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found {
		t.Error("meta key should be deleted from store")
	}
}

func TestMatViewClose(t *testing.T) {
	create := NewCreateMatView("mv", &PS.Select{}, nil)
	if err := create.Close(); err != nil {
		t.Errorf("CreateMatView Close: %v", err)
	}

	drop := NewDropMatView("mv", nil)
	if err := drop.Close(); err != nil {
		t.Errorf("DropMatView Close: %v", err)
	}

	planner := NewPlanner()
	refresh := NewRefreshMatView("mv", nil, nil, planner)
	if err := refresh.Close(); err != nil {
		t.Errorf("RefreshMatView Close: %v", err)
	}
}

func TestMatViewClearAll(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	RegisterMatView("mv1", &PS.Select{})
	RegisterMatView("mv2", &PS.Select{})
	if LookupMatView("mv1") == nil || LookupMatView("mv2") == nil {
		t.Fatal("mat views should be registered")
	}

	UnregisterAllMatViews()
	if LookupMatView("mv1") != nil || LookupMatView("mv2") != nil {
		t.Error("all mat views should be cleared")
	}
}
