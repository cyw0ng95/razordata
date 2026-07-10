package CO

import (
	"errors"
	"fmt"

	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	"github.com/cyw0ng95/razordata/internal/SQO/CP"
	"github.com/cyw0ng95/razordata/internal/SQO/MM"
	"github.com/cyw0ng95/razordata/internal/SQO/RW"
	"github.com/cyw0ng95/razordata/internal/SQO/SL"
)

type PlanBuilder func(stmt PS.Stmt) (DT.Operator, error)

type TableInfo struct {
	Name    string
	Cols    []DT.ColInfo
	PK      string
	Indexes map[string][]string
}

type Optimizer interface {
	Plan(stmt PS.Stmt) (DT.Operator, error)
	SetCostParams(cp CP.CostParams)
	SetStatsCatalog(stats SL.StatsCatalog)
	InvalidateCache()
	RegisterTable(name string, cols []DT.ColInfo, pk string)
	RegisterIndex(table, index string, cols []string)
	SetBuilder(builder PlanBuilder)
}

type optimizer struct {
	cp         CP.CostParams
	stats      SL.StatsCatalog
	cache      *MM.Memo
	builder    PlanBuilder
	tables     map[string]*TableInfo
}

func New(store interface{}) Optimizer {
	return &optimizer{
		cp:     CP.Default(),
		stats:  nil,
		cache:  MM.NewMemo(MM.DefaultMaxMemoEntries),
		tables: make(map[string]*TableInfo),
	}
}

func NewWithOptions(opts Options) Optimizer {
	return &optimizer{
		cp:     opts.CostParams,
		stats:  opts.Stats,
		cache:  MM.NewMemo(opts.CacheSize),
		tables: make(map[string]*TableInfo),
	}
}

type Options struct {
	CostParams CP.CostParams
	Stats      SL.StatsCatalog
	CacheSize  int
}

func (o *optimizer) Plan(stmt PS.Stmt) (DT.Operator, error) {
	if o.builder == nil {
		return nil, errors.New("optimizer: no PlanBuilder set")
	}

	rewritten, err := RW.Rewrite(stmt)
	if err != nil {
		return nil, fmt.Errorf("optimizer: rewrite: %w", err)
	}

	op, err := o.builder(rewritten)
	if err != nil {
		return nil, err
	}

	memoKey := MM.SerializeKey(stmt, o.cache.SchemaVersion())
	o.cache.Put(memoKey, &MM.Plan{Cost: 0, MemoKey: memoKey, SchemaVer: o.cache.SchemaVersion()})

	return op, nil
}

func (o *optimizer) SetCostParams(cp CP.CostParams) {
	o.cp = cp
}

func (o *optimizer) SetStatsCatalog(stats SL.StatsCatalog) {
	o.stats = stats
}

func (o *optimizer) InvalidateCache() {
	o.cache.Clear()
	MM.BumpDefaultSchemaVersion()
}

func (o *optimizer) RegisterTable(name string, cols []DT.ColInfo, pk string) {
	o.tables[name] = &TableInfo{
		Name: name,
		Cols: cols,
		PK:   pk,
	}
}

func (o *optimizer) RegisterIndex(table, index string, cols []string) {
	t, ok := o.tables[table]
	if !ok {
		return
	}
	if t.Indexes == nil {
		t.Indexes = make(map[string][]string)
	}
	t.Indexes[index] = cols
}

func (o *optimizer) SetBuilder(builder PlanBuilder) {
	o.builder = builder
}