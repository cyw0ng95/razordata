package EX

import (
	"context"
	"errors"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type Insert struct {
	table    string
	cols     []string
	values   [][]PS.Expr
	store    Store
	schema   *storeSchema
	txWriter TxWriter
	rows     int64
	done     bool
}

func NewInsert(table string, cols []string, values [][]PS.Expr) *Insert {
	return &Insert{
		table:  table,
		cols:   cols,
		values: values,
	}
}

// NewInsertWithStore builds an Insert that writes through the engine. The
// table must have been registered and must have a primary key column.
func NewInsertWithStore(store Store, table string, cols []string, values [][]PS.Expr) (*Insert, error) {
	ss, ok := schemaFor(table)
	if !ok {
		return nil, errors.New("ex: table not registered: " + table)
	}
	if ss.pk == "" {
		return nil, ErrNoPKForStorage
	}
	return &Insert{
		table:  table,
		cols:   cols,
		values: values,
		store:  store,
		schema: ss,
	}, nil
}

func (i *Insert) Next(ctx context.Context) (Row, error) {
	if i.done {
		return Row{}, ErrNoRows
	}
	i.done = true
	if i.store != nil {
		return i.nextFromStore(ctx)
	}
	schema := Schema(i.table)
	if schema == nil && len(i.cols) > 0 {
		schema = i.cols
	}
	tablesMu.Lock()
	defer tablesMu.Unlock()
	existing := tables[i.table]
	for _, row := range i.values {
		out, err := buildInsertRow(schema, i.cols, row)
		if err != nil {
			return Row{}, err
		}
		existing = append(existing, out)
		i.rows++
	}
	tables[i.table] = existing
	return Row{}, ErrNoRows
}

func (i *Insert) nextFromStore(ctx context.Context) (Row, error) {
	prefix := tablePrefix(i.table)
	for _, row := range i.values {
		out, err := buildInsertRow(i.schema.cols, i.cols, row)
		if err != nil {
			return Row{}, err
		}
		pk, err := extractPK(i.schema, out)
		if err != nil {
			return Row{}, err
		}
		buf, err := encodeRow(i.schema, out)
		if err != nil {
			return Row{}, err
		}
		key := rowKey(prefix, pk)
		if err := i.store.Insert(key, buf); err != nil {
			return Row{}, err
		}
		if i.txWriter != nil {
			i.txWriter.RecordWrite(key, buf)
		}
		i.rows++
	}
	_ = ctx
	return Row{}, ErrNoRows
}

func (i *Insert) Close() error {
	return nil
}

func (i *Insert) RowsAffected() int64 {
	return i.rows
}

type Update struct {
	table    string
	set      []PS.Pair
	where    PS.Expr
	iter     Operator
	store    Store
	schema   *storeSchema
	txWriter TxWriter
	rows     int64
	done     bool
}

func NewUpdate(table string, set []PS.Pair, where PS.Expr, iter Operator) *Update {
	return &Update{table: table, set: set, where: where, iter: iter}
}

// NewUpdateWithStore builds an Update that reads the old row via the engine
// iterator and writes the new version through engine.Insert.
func NewUpdateWithStore(store Store, table string, set []PS.Pair, where PS.Expr, iter Operator) (*Update, error) {
	ss, ok := schemaFor(table)
	if !ok {
		return nil, errors.New("ex: table not registered: " + table)
	}
	if ss.pk == "" {
		return nil, ErrNoPKForStorage
	}
	return &Update{
		table:  table,
		set:    set,
		where:  where,
		iter:   iter,
		store:  store,
		schema: ss,
	}, nil
}

func (u *Update) Next(ctx context.Context) (Row, error) {
	if u.done {
		return Row{}, ErrNoRows
	}
	u.done = true
	if u.store != nil {
		return u.nextFromStore(ctx)
	}
	for {
		row, err := u.iter.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return Row{}, err
		}
		if u.where != nil {
			ok, err := Eval(u.where, &row, nil)
			if err != nil {
				return Row{}, err
			}
			if !truthy(ok) {
				continue
			}
		}
		snapshot := cloneRow(row)
		if err := applyUpdate(&row, u.set); err != nil {
			return Row{}, err
		}
		if err := replaceBySnapshot(u.table, snapshot, row); err != nil {
			return Row{}, err
		}
		u.rows++
	}
	return Row{}, ErrNoRows
}

func (u *Update) nextFromStore(ctx context.Context) (Row, error) {
	prefix := tablePrefix(u.table)
	for {
		row, err := u.iter.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return Row{}, err
		}
		if u.where != nil {
			ok, err := Eval(u.where, &row, nil)
			if err != nil {
				return Row{}, err
			}
			if !truthy(ok) {
				continue
			}
		}
		if err := applyUpdate(&row, u.set); err != nil {
			return Row{}, err
		}
		pk, err := extractPK(u.schema, row)
		if err != nil {
			return Row{}, err
		}
		buf, err := encodeRow(u.schema, row)
		if err != nil {
			return Row{}, err
		}
		key := rowKey(prefix, pk)
		if err := u.store.Insert(key, buf); err != nil {
			return Row{}, err
		}
		if u.txWriter != nil {
			u.txWriter.RecordWrite(key, buf)
		}
		u.rows++
	}
	return Row{}, ErrNoRows
}

func (u *Update) Close() error {
	return u.iter.Close()
}

func (u *Update) RowsAffected() int64 {
	return u.rows
}

type Delete struct {
	table    string
	where    PS.Expr
	iter     Operator
	store    Store
	schema   *storeSchema
	txWriter TxWriter
	rows     int64
	done     bool
}

func NewDelete(table string, where PS.Expr, iter Operator) *Delete {
	return &Delete{table: table, where: where, iter: iter}
}

// NewDeleteWithStore builds a Delete that removes rows through engine.Delete.
func NewDeleteWithStore(store Store, table string, where PS.Expr, iter Operator) (*Delete, error) {
	ss, ok := schemaFor(table)
	if !ok {
		return nil, errors.New("ex: table not registered: " + table)
	}
	if ss.pk == "" {
		return nil, ErrNoPKForStorage
	}
	return &Delete{
		table:  table,
		where:  where,
		iter:   iter,
		store:  store,
		schema: ss,
	}, nil
}

func (d *Delete) Next(ctx context.Context) (Row, error) {
	if d.done {
		return Row{}, ErrNoRows
	}
	d.done = true
	if d.store != nil {
		return d.nextFromStore(ctx)
	}
	toDelete := map[int]bool{}
	for {
		row, err := d.iter.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return Row{}, err
		}
		if d.where != nil {
			ok, err := Eval(d.where, &row, nil)
			if err != nil {
				return Row{}, err
			}
			if !truthy(ok) {
				continue
			}
		}
		idx, ok := rowIndex(d.table, row)
		if ok {
			toDelete[idx] = true
		}
	}
	if len(toDelete) > 0 {
		tablesMu.Lock()
		defer tablesMu.Unlock()
		existing := tables[d.table]
		out := existing[:0]
		for i, r := range existing {
			if !toDelete[i] {
				out = append(out, r)
			}
		}
		tables[d.table] = out
		d.rows = int64(len(toDelete))
	}
	return Row{}, ErrNoRows
}

func (d *Delete) nextFromStore(ctx context.Context) (Row, error) {
	prefix := tablePrefix(d.table)
	for {
		row, err := d.iter.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return Row{}, err
		}
		if d.where != nil {
			ok, err := Eval(d.where, &row, nil)
			if err != nil {
				return Row{}, err
			}
			if !truthy(ok) {
				continue
			}
		}
		pk, err := extractPK(d.schema, row)
		if err != nil {
			return Row{}, err
		}
		key := rowKey(prefix, pk)
		if err := d.store.Delete(key); err != nil {
			return Row{}, err
		}
		if d.txWriter != nil {
			d.txWriter.RecordWrite(key, nil)
		}
		d.rows++
	}
	return Row{}, ErrNoRows
}

func (d *Delete) Close() error {
	return d.iter.Close()
}

func (d *Delete) RowsAffected() int64 {
	return d.rows
}

type CreateTable struct {
	stmt *PS.CreateTable
	done bool
}

func NewCreateTable(stmt *PS.CreateTable) *CreateTable {
	return &CreateTable{stmt: stmt}
}

func (c *CreateTable) Next(ctx context.Context) (Row, error) {
	if c.done {
		return Row{}, ErrNoRows
	}
	c.done = true
	tablesMu.Lock()
	if _, ok := tables[c.stmt.Name]; ok {
		tablesMu.Unlock()
		return Row{}, errTableExists
	}
	cols := make([]string, len(c.stmt.Cols))
	for i, col := range c.stmt.Cols {
		cols[i] = col.Name
	}
	tables[c.stmt.Name] = []Row{}
	schemas[c.stmt.Name] = cols
	tablesMu.Unlock()
	var pk string
	if c.stmt.PK != nil {
		pk = *c.stmt.PK
	}
	registerStoreSchema(c.stmt.Name, cols, pk)
	return Row{}, ErrNoRows
}

func (c *CreateTable) Close() error {
	return nil
}

type DropTable struct {
	stmt *PS.DropTable
	done bool
	rows int64
}

func NewDropTable(stmt *PS.DropTable) *DropTable {
	return &DropTable{stmt: stmt}
}

func (d *DropTable) Next(ctx context.Context) (Row, error) {
	if d.done {
		return Row{}, ErrNoRows
	}
	d.done = true
	tablesMu.Lock()
	if existing, ok := tables[d.stmt.Name]; ok {
		d.rows = int64(len(existing))
		delete(tables, d.stmt.Name)
	}
	tablesMu.Unlock()
	// Drop the store schema mapping; actual data is left in the engine and
	// unreachable until the same tableID is reused.
	storeMu.Lock()
	if id, ok := tableIDs[d.stmt.Name]; ok {
		delete(storeSchemas, id)
		delete(tableIDs, d.stmt.Name)
	}
	storeMu.Unlock()
	return Row{}, ErrNoRows
}

func (d *DropTable) Close() error {
	return nil
}

func (d *DropTable) RowsAffected() int64 {
	return d.rows
}
