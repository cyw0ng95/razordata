package EX

import (
	"context"
	"fmt"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
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

func (c *CreateViewOperator) Next(_ context.Context) (DT.Row, error) {
	if c.done {
		return DT.Row{}, DT.ErrNoRows
	}
	c.done = true

	// REQ000825: reject duplicate view names.
	if DT.LookupView(c.stmt.Name) != nil {
		return DT.Row{}, fmt.Errorf("ex: view %q already exists", c.stmt.Name)
	}

	sel, ok := c.stmt.As.(*PS.Select)
	if !ok {
		return DT.Row{}, EV.ErrEval
	}
	DT.RegisterView(c.stmt.Name, sel)

	return DT.Row{
		Cols: []string{"result"},
		Data: []DT.Value{NewTextValue("view created")},
	}, nil
}

func (c *CreateViewOperator) Close() error { return nil }
