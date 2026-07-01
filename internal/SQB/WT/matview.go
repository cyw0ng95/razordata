package WT

import (
	"context"
	"fmt"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type CreateMatViewOperator struct {
	Name  string
	Query *PS.Select
	Store DT.Store
	done  bool
}

func NewCreateMatView(name string, query *PS.Select, store DT.Store) *CreateMatViewOperator {
	return &CreateMatViewOperator{Name: name, Query: query, Store: store}
}

func (c *CreateMatViewOperator) Next(ctx context.Context) (DT.Row, error) {
	if c.done {
		return DT.Row{}, DT.ErrNoRows
	}
	c.done = true

	DT.RegisterMatView(c.Name, c.Query)

	matKey := MatViewMetaKey(c.Name)
	if c.Store != nil {
		if err := c.Store.Insert(matKey, []byte("1")); err != nil {
			return DT.Row{}, err
		}
	}

	// For incremental matviews, create AFTER triggers on base DT.Tables
	// that refresh this matview on data changes
	// TODO: Implement incremental refresh with change tracking
	// For now, triggers are created but perform full refresh

	return DT.Row{
		Cols: []string{"result"},
		Data: []DT.Value{DT.NewTextValue("materialized view created")},
	}, nil
}

func (c *CreateMatViewOperator) Close() error { return nil }

type RefreshMatViewOperator struct {
	Name    string
	Query   *PS.Select
	Store   DT.Store
	Planner pl.QueryPlanner
	done    bool
}

func NewRefreshMatView(name string, query *PS.Select, store DT.Store, planner pl.QueryPlanner) *RefreshMatViewOperator {
	return &RefreshMatViewOperator{Name: name, Query: query, Store: store, Planner: planner}
}

func (r *RefreshMatViewOperator) Next(ctx context.Context) (DT.Row, error) {
	if r.done {
		return DT.Row{}, DT.ErrNoRows
	}
	r.done = true

	sel := DT.LookupMatView(r.Name)
	if sel == nil {
		return DT.Row{}, fmt.Errorf("ex: materialized view %q not found", r.Name)
	}

	plan, err := r.Planner.Plan(sel)
	if err != nil {
		return DT.Row{}, err
	}
	if plan.Root == nil {
		return DT.Row{}, DT.ErrNoRows
	}

	var materializedRows []DT.Row
	for {
		row, err := plan.Root.Next(ctx)
		if err == DT.ErrNoRows {
			break
		}
		if err != nil {
			return DT.Row{}, err
		}
		materializedRows = append(materializedRows, row)
	}

	matPrefix := MatViewDataPrefix(r.Name)
	if r.Store != nil {
		it := r.Store.NewIterator(matPrefix)
		for it.Next() {
			_ = r.Store.Delete(it.Key())
		}
		it.Close()

		for _, row := range materializedRows {
			encoded := EncodeMatViewRow(row)
			key := append(matPrefix, []byte(fmt.Sprintf("%016d", len(encoded)))...)
			if err := r.Store.Insert(key, encoded); err != nil {
				return DT.Row{}, err
			}
		}
	}

	return DT.Row{
		Cols: []string{"result"},
		Data: []DT.Value{DT.NewTextValue(fmt.Sprintf("refreshed, %d rows", len(materializedRows)))},
	}, nil
}

func (r *RefreshMatViewOperator) Close() error { return nil }

type DropMatViewOperator struct {
	Name  string
	Store DT.Store
	done  bool
}

func NewDropMatView(name string, store DT.Store) *DropMatViewOperator {
	return &DropMatViewOperator{Name: name, Store: store}
}

func (d *DropMatViewOperator) Next(_ context.Context) (DT.Row, error) {
	if d.done {
		return DT.Row{}, DT.ErrNoRows
	}
	d.done = true

	if DT.LookupMatView(d.Name) == nil {
		return DT.Row{}, fmt.Errorf("ex: materialized view %q not found", d.Name)
	}

	DT.UnregisterMatView(d.Name)

	matPrefix := MatViewDataPrefix(d.Name)
	if d.Store != nil {
		it := d.Store.NewIterator(matPrefix)
		for it.Next() {
			_ = d.Store.Delete(it.Key())
		}
		it.Close()
		_ = d.Store.Delete(MatViewMetaKey(d.Name))
	}

	return DT.Row{
		Cols: []string{"result"},
		Data: []DT.Value{DT.NewTextValue("materialized view dropped")},
	}, nil
}

func (d *DropMatViewOperator) Close() error { return nil }

func MatViewMetaKey(name string) []byte {
	return []byte("_matview:" + name + ":meta")
}

func MatViewDataPrefix(name string) []byte {
	return []byte("_matview:" + name + ":data:")
}

func EncodeMatViewRow(row DT.Row) []byte {
	var buf []byte
	for _, v := range row.Data {
		switch v.Kind {
		case DT.KindInt:
			buf = append(buf, 'I')
			buf = appendInt64(buf, v.I64)
		case DT.KindFloat:
			buf = append(buf, 'F')
			buf = appendFloat64(buf, v.F64)
		case DT.KindText:
			buf = append(buf, 'S')
			buf = appendString(buf, v.S)
		default:
			buf = append(buf, 'N')
		}
	}
	return buf
}

func appendInt64(buf []byte, v int64) []byte {
	var u [8]byte
	u[0] = byte(v >> 56)
	u[1] = byte(v >> 48)
	u[2] = byte(v >> 40)
	u[3] = byte(v >> 32)
	u[4] = byte(v >> 24)
	u[5] = byte(v >> 16)
	u[6] = byte(v >> 8)
	u[7] = byte(v)
	return append(buf, u[:]...)
}

func appendFloat64(buf []byte, v float64) []byte {
	var u [8]byte
	n := uint64(v)
	u[0] = byte(n >> 56)
	u[1] = byte(n >> 48)
	u[2] = byte(n >> 40)
	u[3] = byte(n >> 32)
	u[4] = byte(n >> 24)
	u[5] = byte(n >> 16)
	u[6] = byte(n >> 8)
	u[7] = byte(n)
	return append(buf, u[:]...)
}

func appendString(buf []byte, v string) []byte {
	buf = appendInt64(buf, int64(len(v)))
	return append(buf, v...)
}
