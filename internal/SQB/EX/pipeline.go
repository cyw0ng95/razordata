package EX

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// buildPipeline creates a Pipeline from a chain of row-based Operators.
// Each operator is wrapped in a PipelineOperator adapter.
func buildPipeline(ops []Operator, bufSize int) *UT.Pipeline {
	stages := make([]UT.PipelineOperator, len(ops))
	for i, op := range ops {
		stages[i] = &operatorPipelineAdapter{op: op}
	}
	return UT.NewPipeline(stages, bufSize)
}

// operatorPipelineAdapter wraps a row-based Operator as a PipelineOperator.
type operatorPipelineAdapter struct {
	op      Operator
	batch   *UT.Batch
	drained bool
}

func (a *operatorPipelineAdapter) Process(ctx context.Context, batch *UT.Batch) (*UT.Batch, error) {
	if a.drained {
		return nil, nil
	}
	a.drained = true
	var rows []Row
	for {
		r, err := a.op.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return nil, err
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	nCols := len(rows[0].Cols)
	out := UT.GetBatch(nCols)
	for i, name := range rows[0].Cols {
		out.SetColumnName(i, name)
	}
	for _, r := range rows {
		for ci := range r.Cols {
			out.AppendRow(ci, kindToTokenType(int(r.Data[ci].Kind)), r.Data[ci].ToAny(), r.Data[ci].IsNull())
		}
		out.AdvanceSize()
	}
	a.batch = out
	return out, nil
}

func (a *operatorPipelineAdapter) Close() error { return a.op.Close() }

// kindToTokenType maps ValueKind to LX.TokenType for batch conversions.
func kindToTokenType(k int) LX.TokenType {
	switch k {
	case 0: // KindNull
		return LX.T_NULL
	case 1: // KindInt
		return LX.T_INT_KW
	case 2: // KindFloat
		return LX.T_FLOAT_KW
	case 3: // KindText
		return LX.T_TEXT
	case 4: // KindBlob
		return LX.T_BLOB
	case 5: // KindBool
		return LX.T_BOOL
	default:
		return LX.T_NULL
	}
}
