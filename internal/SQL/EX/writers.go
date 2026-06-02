package EX

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type Insert struct {
	table  string
	cols   []string
	values [][]PS.Expr
}

func NewInsert(table string, cols []string, values [][]PS.Expr) *Insert {
	return &Insert{
		table:  table,
		cols:   cols,
		values: values,
	}
}

func (i *Insert) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNotImplemented
}

func (i *Insert) Close() error {
	return nil
}

type Update struct {
	table string
	set   []PS.Pair
	where PS.Expr
	iter  Operator
}

func NewUpdate(table string, set []PS.Pair, where PS.Expr, iter Operator) *Update {
	return &Update{
		table: table,
		set:   set,
		where: where,
		iter:  iter,
	}
}

func (u *Update) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNotImplemented
}

func (u *Update) Close() error {
	return u.iter.Close()
}

type Delete struct {
	table string
	where PS.Expr
	iter  Operator
}

func NewDelete(table string, where PS.Expr, iter Operator) *Delete {
	return &Delete{
		table: table,
		where: where,
		iter:  iter,
	}
}

func (d *Delete) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNotImplemented
}

func (d *Delete) Close() error {
	return d.iter.Close()
}

type CreateTable struct {
	stmt *PS.CreateTable
}

func NewCreateTable(stmt *PS.CreateTable) *CreateTable {
	return &CreateTable{stmt: stmt}
}

func (c *CreateTable) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNotImplemented
}

func (c *CreateTable) Close() error {
	return nil
}

type DropTable struct {
	stmt *PS.DropTable
}

func NewDropTable(stmt *PS.DropTable) *DropTable {
	return &DropTable{stmt: stmt}
}

func (d *DropTable) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNotImplemented
}

func (d *DropTable) Close() error {
	return nil
}
