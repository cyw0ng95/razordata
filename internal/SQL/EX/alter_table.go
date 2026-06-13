package EX

import (
	"context"

	PS "github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// AlterTable implements ALTER TABLE DDL operations.
// REQ000441: ALTER TABLE ADD COLUMN routing.
type AlterTable struct {
	stmt *PS.AlterTableStmt
}

func NewAlterTable(stmt *PS.AlterTableStmt) *AlterTable {
	return &AlterTable{stmt: stmt}
}

func (a *AlterTable) Next(ctx context.Context) (Row, error) {
	// ALTER TABLE is a DDL operation that modifies schema.
	// For now, return a single empty row to indicate success.
	// Schema changes are not yet implemented (REQ000244/REQ000129).
	return Row{}, ErrNoRows
}

func (a *AlterTable) Close() error {
	return nil
}
