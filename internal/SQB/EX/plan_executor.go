package EX

import (
	"context"
	"errors"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQB/AD"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// planWithCache returns a compiled plan for stmt, checking the plan
// cache first. On cache miss, plans via Planner.Plan and caches the
// result. REQ001011.
// REQ001195: parameterized cache key so queries with the same
// structure but different literal values share a single cache entry.
// On cache hit, comparison-literals in the plan tree are replaced
// via replaceLiteralsOnTree using the current query's extracted values.
func (e *Executor) planWithCache(stmt PS.Stmt) (*pl.PlanResult, error) {
	if e.optimizer != nil {
		root, err := e.optimizer.Plan(stmt)
		if err != nil {
			return nil, err
		}
		if root != nil {
			ResolvePlanSlots(root)
		}
		return &pl.PlanResult{Root: root}, nil
	}
	if e.planCache.entries != nil {
		paramStmt, params := pl.NormalizeForMemo(stmt)
		if paramStmt == nil {
			paramStmt = stmt
		}
		key := pl.SerializeKey(paramStmt)
		if cached := e.getCachedPlan(key); cached != nil {
			replaceLiteralsOnTree(cached.Root, params)
			return cached, nil
		}
		plan, err := e.planner.Plan(stmt)
		if err != nil {
			return nil, err
		}
		if plan == nil || plan.Root == nil {
			return nil, errors.New("ex: plan produced no root")
		}
		ResolvePlanSlots(plan.Root)
		e.putCachedPlan(key, plan)
		return plan, nil
	}
	plan, err := e.planner.Plan(stmt)
	if err != nil {
		return nil, err
	}
	if plan != nil && plan.Root != nil {
		ResolvePlanSlots(plan.Root)
	}
	return plan, nil
}

// ExtractParamTypes parses sql and returns the SQL column type
// of each `?` placeholder in left-to-right order. Entries are
// ls.CTInt / ls.CTVarchar / etc. when the placeholder can be
// resolved to a known column; -1 otherwise. This is the ST
// layer's source of truth for per-placeholder Go-type
// validation (R16-3, R16-4).
func (e *Executor) ExtractParamTypes(sql string) []int {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil
	}
	out := []int{}
	walkPlaceholderTypes(stmt, &out)
	return out
}

func (e *Executor) Exec(ctx context.Context, sql string, args ...any) (Result, error) {
	// Try cache first (P0: StmtCache wiring, saves 12.70% CPU on parsing)
	if e.stmtCache.entries != nil {
		if cached := e.getCachedStmt(sql); cached != nil {
			stmt := cached
			// Check if this is a DML with RETURNING clause
			if hasReturning(stmt) {
				op, err := e.buildWriterOp(stmt)
				if err != nil {
					return Result{}, err
				}
				propagateParams(op, args)
				defer op.Close()
				var count int64
				for {
					_, err := op.Next(ctx)
					if err != nil {
						if err == DT.ErrNoRows {
							break
						}
						return Result{}, err
					}
					count++
				}
				return Result{RowsAffected: count}, nil
			}

			op, err := e.buildWriterOp(stmt)
			if err != nil {
				return Result{}, err
			}
			propagateParams(op, args)
			propagatePlanner(op, e.planner)
			execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: 0, TotalChanges: e.totalChanges}
			propagateExecContext(op, execCtx)
			defer resetRowArena(execCtx)
			defer op.Close()
			if _, err := op.Next(ctx); err != nil && err != DT.ErrNoRows {
				return Result{}, err
			}
			e.lastChanges = execCtx.LastChanges
			e.totalChanges = execCtx.TotalChanges
			return extractResult(op)
		}
	}

	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return Result{}, err
	}
	// Cache the parsed statement
	if e.stmtCache.entries != nil {
		e.putCachedStmt(sql, stmt)
	}

	// Check if this is a DML with RETURNING clause
	if hasReturning(stmt) {
		op, err := e.buildWriterOp(stmt)
		if err != nil {
			return Result{}, err
		}
		propagateParams(op, args)
		defer op.Close()
		var count int64
		for {
			_, err := op.Next(ctx)
			if err != nil {
				if err == DT.ErrNoRows {
					break
				}
				return Result{}, err
			}
			count++
		}
		return Result{RowsAffected: count}, nil
	}

	op, err := e.buildWriterOp(stmt)
	if err != nil {
		return Result{}, err
	}
	// R16-1: thread args down to the operator tree so `?`
	// placeholders resolve. Writers (INSERT/UPDATE/DELETE) also
	// support placeholders (e.g. INSERT ... VALUES (?,?)).
	propagateParams(op, args)
	propagatePlanner(op, e.planner)
	execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: 0, TotalChanges: e.totalChanges}
	propagateExecContext(op, execCtx)
	defer resetRowArena(execCtx)
	if _, err := op.Next(ctx); err != nil && err != DT.ErrNoRows {
		return Result{}, err
	}
	e.lastChanges = execCtx.LastChanges
	e.totalChanges = execCtx.TotalChanges
	return extractResult(op)
}

func (e *Executor) Query(ctx context.Context, sql string, args ...any) (*Rows, error) {
	// Try cache first (P0: StmtCache wiring, saves 12.70% CPU on parsing)
	if e.stmtCache.entries != nil {
		if cached := e.getCachedStmt(sql); cached != nil {
			stmt := cached
			// Check if this is a DML with RETURNING clause
			if hasReturning(stmt) {
				op, err := e.buildWriterOp(stmt)
				if err != nil {
					return nil, err
				}
				propagateParams(op, args)
				defer op.Close()
				var out []DT.Row
				for {
					row, err := op.Next(ctx)
					if err != nil {
						if err == DT.ErrNoRows {
							break
						}
						return nil, err
					}
					out = append(out, row)
				}
				if len(out) == 0 {
					return &Rows{}, nil
				}
				return &Rows{Cols: append([]string(nil), out[0].Cols...), Types: append([]LX.TokenType(nil), out[0].Types...)}, nil
			}

			plan, err := e.planWithCache(stmt)
			if err != nil {
				return nil, err
			}
			if plan == nil || plan.Root == nil {
				return nil, errors.New("ex: plan produced no root")
			}
			propagateParams(plan.Root, args)
			propagatePlanner(plan.Root, e.planner)
			execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
			propagateExecContext(plan.Root, execCtx)
			defer resetRowArena(execCtx)
			defer plan.Root.Close()
			row, err := plan.Root.Next(ctx)
			if err != nil {
				if err == DT.ErrNoRows {
					return &Rows{}, nil
				}
				return nil, err
			}
			DT.WithExecContext(&row, execCtx)
			rs := &Rows{Cols: append([]string(nil), row.Cols...), Types: append([]LX.TokenType(nil), row.Types...)}
			return rs, nil
		}
	}

	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil, err
	}
	// Cache the parsed statement
	if e.stmtCache.entries != nil {
		e.putCachedStmt(sql, stmt)
	}

	// Check if this is a DML with RETURNING clause
	if hasReturning(stmt) {
		op, err := e.buildWriterOp(stmt)
		if err != nil {
			return nil, err
		}
		propagateParams(op, args)
		defer op.Close()
		var out []DT.Row
		for {
			row, err := op.Next(ctx)
			if err != nil {
				if err == DT.ErrNoRows {
					break
				}
				return nil, err
			}
			out = append(out, row)
		}
		if len(out) == 0 {
			return &Rows{}, nil
		}
		return &Rows{Cols: append([]string(nil), out[0].Cols...), Types: append([]LX.TokenType(nil), out[0].Types...)}, nil
	}

	plan, err := e.planWithCache(stmt)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.Root == nil {
		return nil, errors.New("ex: plan produced no root")
	}
	// R16-1: thread args down to the operator tree so `?`
	// placeholders resolve during Eval.
	propagateParams(plan.Root, args)
	propagatePlanner(plan.Root, e.planner)
	execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	propagateExecContext(plan.Root, execCtx)
	defer resetRowArena(execCtx)
	// Attempt vectorized execution for eligible query plans.
	plan.Root = tryVectorizePlan(plan.Root)
	defer plan.Root.Close()
	row, err := plan.Root.Next(ctx)
	if err != nil {
		if err == DT.ErrNoRows {
			return &Rows{}, nil
		}
		return nil, err
	}
	DT.WithExecContext(&row, execCtx)
	rs := &Rows{Cols: append([]string(nil), row.Cols...), Types: append([]LX.TokenType(nil), row.Types...)}
	return rs, nil
}

func (e *Executor) QueryAll(ctx context.Context, sql string, args ...any) ([]DT.Row, error) {
	// Try cache first (P0: StmtCache wiring, saves 12.70% CPU on parsing)
	if e.stmtCache.entries != nil {
		if cached := e.getCachedStmt(sql); cached != nil {
			stmt := cached
			plan, err := e.planWithCache(stmt)
			if err != nil {
				return nil, err
			}
			if plan == nil || plan.Root == nil {
				return nil, errors.New("ex: plan produced no root")
			}
			propagateParams(plan.Root, args)
			propagatePlanner(plan.Root, e.planner)
			execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
			propagateExecContext(plan.Root, execCtx)
			defer resetRowArena(execCtx)
			defer plan.Root.Close()
			var out []DT.Row
			for {
				row, err := plan.Root.Next(ctx)
				if err != nil {
					if err == DT.ErrNoRows {
						break
					}
					return nil, err
				}
				DT.WithExecContext(&row, execCtx)
				out = append(out, row)
			}
			return out, nil
		}
	}

	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil, err
	}
	// Cache the parsed statement
	if e.stmtCache.entries != nil {
		e.putCachedStmt(sql, stmt)
	}
	plan, err := e.planWithCache(stmt)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.Root == nil {
		return nil, errors.New("ex: plan produced no root")
	}
	propagateParams(plan.Root, args)
	// REQ000366: thread the main-plan planner so SeqScan rows
	// carry it into subquery evals. propagatePlanner is a
	// depth-first walk that calls WithPlanner on every node
	// that supports it.
	propagatePlanner(plan.Root, e.planner)
	// REQ000586: thread DT.ExecContext through rows to eliminate
	// the global currentSubqueryPlanner.
	execCtx := &DT.ExecContext{Planner: e.planner, SessionID: DT.GetCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	propagateExecContext(plan.Root, execCtx)
	defer resetRowArena(execCtx)
	defer plan.Root.Close()
	var out []DT.Row
	for {
		row, err := plan.Root.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return nil, err
		}
		DT.WithExecContext(&row, execCtx)
		out = append(out, row)
	}
	return out, nil
}

// Explain plans the statement and returns a human-readable
// description of the operator tree. The plan is closed before
// returning, so Explain does not run the query.
func (e *Executor) Explain(sql string) (string, error) {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return "", err
	}
	plan, err := e.planWithCache(stmt)
	if err != nil {
		return "", err
	}
	if plan == nil || plan.Root == nil {
		return "", errors.New("ex: plan produced no root")
	}
	defer plan.Root.Close()
	// Use formatPlanTree with default ExplainNormal mode for text output
	nodes := buildPlanNodeTree(plan.Root, e.planner)
	var b strings.Builder
	var walk func(node *AD.PlanNode, depth int)
	walk = func(node *AD.PlanNode, depth int) {
		if node == nil {
			return
		}
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteString(node.Type)
		if node.Table != "" {
			b.WriteString(" ")
			b.WriteString(node.Table)
		}
		if node.Detail != "" {
			b.WriteString(" ")
			b.WriteString(node.Detail)
		}
		b.WriteByte('\n')
		for _, child := range node.Children {
			walk(child, depth+1)
		}
	}
	walk(nodes, 0)
	return b.String(), nil
}

// tryVectorizePlan is declared external (vec_transform.go) so the
// linker resolves it; no-op if vec_transform is not compiled in.
// see internal/SQB/EX/vec_transform.go
// func tryVectorizePlan(root DT.Operator) DT.Operator