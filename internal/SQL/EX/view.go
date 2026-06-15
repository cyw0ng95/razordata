package EX

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// CreateViewOperator registers a view definition (REQ000240).
type CreateViewOperator struct {
	stmt *PS.CreateViewStmt
	done bool
}

// NewCreateView creates a CreateView operator.
func NewCreateView(stmt *PS.CreateViewStmt) *CreateViewOperator {
	return &CreateViewOperator{stmt: stmt}
}

func (c *CreateViewOperator) Next(_ context.Context) (Row, error) {
	if c.done {
		return Row{}, ErrNoRows
	}
	c.done = true

	sel, ok := c.stmt.As.(*PS.Select)
	if !ok {
		return Row{}, ErrEval
	}
	RegisterView(c.stmt.Name, sel)

	return Row{
		Cols: []string{"result"},
		Data: []interface{}{"view created"},
	}, nil
}

func (c *CreateViewOperator) Close() error { return nil }
