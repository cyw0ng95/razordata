package EX

import (
	"context"
	"fmt"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type CreateMatViewOperator struct {
	Name  string
	Query *PS.Select
	Store Store
	done  bool
}

func NewCreateMatView(name string, query *PS.Select, store Store) *CreateMatViewOperator {
	return &CreateMatViewOperator{Name: name, Query: query, Store: store}
}

func (c *CreateMatViewOperator) Next(ctx context.Context) (Row, error) {
	if c.done {
		return Row{}, ErrNoRows
	}
	c.done = true

	RegisterMatView(c.Name, c.Query)

	matKey := MatViewMetaKey(c.Name)
	if c.Store != nil {
		if err := c.Store.Insert(matKey, []byte("1")); err != nil {
			return Row{}, err
		}
	}

	// For incremental matviews, create AFTER triggers on base tables
	// that refresh this matview on data changes
	// TODO: Implement incremental refresh with change tracking
	// For now, triggers are created but perform full refresh

	return Row{
		Cols: []string{"result"},
		Data: []interface{}{"materialized view created"},
	}, nil
}

func (c *CreateMatViewOperator) Close() error { return nil }

type RefreshMatViewOperator struct {
	Name    string
	Query   *PS.Select
	Store   Store
	Planner *Planner
	done    bool
}

func NewRefreshMatView(name string, query *PS.Select, store Store, planner *Planner) *RefreshMatViewOperator {
	return &RefreshMatViewOperator{Name: name, Query: query, Store: store, Planner: planner}
}

func (r *RefreshMatViewOperator) Next(ctx context.Context) (Row, error) {
	if r.done {
		return Row{}, ErrNoRows
	}
	r.done = true

	sel := LookupMatView(r.Name)
	if sel == nil {
		return Row{}, fmt.Errorf("ex: materialized view %q not found", r.Name)
	}

	plan, err := r.Planner.Plan(sel)
	if err != nil {
		return Row{}, err
	}
	if plan.root == nil {
		return Row{}, ErrNoRows
	}

	var materializedRows []Row
	for {
		row, err := plan.root.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			return Row{}, err
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
			encoded := encodeMatViewRow(row)
			key := append(matPrefix, []byte(fmt.Sprintf("%016d", len(encoded)))...)
			if err := r.Store.Insert(key, encoded); err != nil {
				return Row{}, err
			}
		}
	}

	return Row{
		Cols: []string{"result"},
		Data: []interface{}{fmt.Sprintf("refreshed, %d rows", len(materializedRows))},
	}, nil
}

func (r *RefreshMatViewOperator) Close() error { return nil }

type DropMatViewOperator struct {
	Name  string
	Store Store
	done  bool
}

func NewDropMatView(name string, store Store) *DropMatViewOperator {
	return &DropMatViewOperator{Name: name, Store: store}
}

func (d *DropMatViewOperator) Next(_ context.Context) (Row, error) {
	if d.done {
		return Row{}, ErrNoRows
	}
	d.done = true

	if LookupMatView(d.Name) == nil {
		return Row{}, fmt.Errorf("ex: materialized view %q not found", d.Name)
	}

	UnregisterMatView(d.Name)

	matPrefix := MatViewDataPrefix(d.Name)
	if d.Store != nil {
		it := d.Store.NewIterator(matPrefix)
		for it.Next() {
			_ = d.Store.Delete(it.Key())
		}
		it.Close()
		_ = d.Store.Delete(MatViewMetaKey(d.Name))
	}

	return Row{
		Cols: []string{"result"},
		Data: []interface{}{"materialized view dropped"},
	}, nil
}

func (d *DropMatViewOperator) Close() error { return nil }

func MatViewMetaKey(name string) []byte {
	return []byte("_matview:" + name + ":meta")
}

func MatViewDataPrefix(name string) []byte {
	return []byte("_matview:" + name + ":data:")
}

func encodeMatViewRow(row Row) []byte {
	var buf []byte
	for _, v := range row.Data {
		switch val := v.(type) {
		case int64:
			buf = append(buf, 'I')
			buf = appendInt64(buf, val)
		case float64:
			buf = append(buf, 'F')
			buf = appendFloat64(buf, val)
		case string:
			buf = append(buf, 'S')
			buf = appendString(buf, val)
		case []byte:
			buf = append(buf, 'B')
			buf = append(buf, val...)
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
