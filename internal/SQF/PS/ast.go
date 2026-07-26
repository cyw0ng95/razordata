package PS

import (
	"reflect"
	"strconv"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// Loc holds source position information for an AST node (REQ001004).
type Loc struct {
	Line uint32
	Col  uint32
}

// Expr is the interface for all expression AST nodes.
type Expr interface {
	exprNode()
}

// Stmt is the interface for all statement AST nodes.
type Stmt interface {
	stmtNode()
}

type NumberLiteral struct {
	Loc
	Val int64
}

func (n *NumberLiteral) exprNode() {}

type FloatLiteral struct {
	Loc
	Val float64
}

func (f *FloatLiteral) exprNode() {}

type StringLiteral struct {
	Loc
	Val string
}

func (s *StringLiteral) exprNode() {}

type BoolLiteral struct {
	Loc
	Val bool
}

func (b *BoolLiteral) exprNode() {}

type NullLiteral struct {
	Loc
}

func (n *NullLiteral) exprNode() {}

type Ident struct {
	Loc
	Name    string
	SlotIdx int // -1 = not resolved; 0+ = index into row.Data for direct access
}

func (i *Ident) exprNode() {}

type QualifiedName struct {
	Loc
	Database  string
	Table     string
	Name      string
	CachedKey string
	SlotIdx   int // -1 = not resolved; 0+ = index into row.Data for direct access
}

func (q *QualifiedName) exprNode() {}

type AliasedExpr struct {
	Loc
	Expr  Expr
	Alias string
}

func (a *AliasedExpr) exprNode() {}

type CastExpr struct {
	Loc
	Expr Expr
	Type *TypeInfo
}

func (c *CastExpr) exprNode() {}

type Param struct {
	Loc
	Index int
}

func (p *Param) exprNode() {}

type BinaryExpr struct {
	Loc
	Op     LX.TokenType
	Left   Expr
	Right  Expr
	Escape Expr
}

func (b *BinaryExpr) exprNode() {}

type UnaryExpr struct {
	Loc
	Op      LX.TokenType
	Operand Expr
}

func (u *UnaryExpr) exprNode() {}

type FunctionCall struct {
	Loc
	Name string
	Args []Expr
}

func (f *FunctionCall) exprNode() {}

type AggregateFunc struct {
	Loc
	Name      string
	Arg       Expr
	Distinct  bool
	Separator Expr
	Filter    Expr

	// REQ001975: lookupKey caches the per-AggregateFunc lookup key
	// used by DT.AggregateLookupKey. Computed once on first access
	// via sync.Once; the AST node is stable within a query so this
	// is safe. Eliminates a fmt.Sprintf per call (~0.85M alloc
	// objects in SLT hot paths).
	lookupKeyOnce sync.Once
	lookupKey     string
}

func (a *AggregateFunc) exprNode() {}

// LookupKey returns a stable per-AggregateFunc key suitable for
// storing/looking up the aggregate's result in a virtual row.
// Computed once and cached. REQ001975.
func (a *AggregateFunc) LookupKey() string {
	a.lookupKeyOnce.Do(func() {
		a.lookupKey = computeAggregateLookupKey(a)
	})
	return a.lookupKey
}

// computeAggregateLookupKey mirrors the previous DT.AggregateLookupKey
// logic but without fmt.Sprintf: StarExpr → "NAME(*)", Ident →
// "NAME(arg)", everything else → "NAME(reflectType:ptr)" using
// reflect.Type.String and strconv on the pointer. REQ001975.
// REQ001730: includes the Distinct flag so SUM(x) and SUM(DISTINCT x)
// produce different keys, preventing virtual-row collisions and
// wrong results when both appear in the same SELECT list.
func computeAggregateLookupKey(a *AggregateFunc) string {
	prefix := a.Name
	if a.Distinct {
		prefix = "DISTINCT " + prefix
	}
	if _, ok := a.Arg.(*StarExpr); ok {
		return prefix + "(*)"
	}
	if ident, ok := a.Arg.(*Ident); ok {
		return prefix + "(" + ident.Name + ")"
	}
	tName := reflect.TypeOf(a.Arg).String()
	ptr := strconv.FormatUint(uint64(reflect.ValueOf(a.Arg).Pointer()), 16)
	return prefix + "(" + tName + ":" + ptr + ")"
}

type WindowSpec struct {
	Loc
	PartitionBy []Expr
	OrderBy     []OrderItem
	Frame       *WindowFrame
}

type WindowFrame struct {
	Loc
	Type    string
	Start   FrameBound
	End     FrameBound
	Exclude string
}

type FrameBound struct {
	Loc
	Type   string
	Offset Expr
}

type WindowFunc struct {
	Loc
	Name string
	Args []Expr
	Over *WindowSpec
}

func (w *WindowFunc) exprNode() {}

type StarExpr struct {
	Loc
}

func (s *StarExpr) exprNode() {}

type ListExpr struct {
	Loc
	Items []Expr
}

func (l *ListExpr) exprNode() {}

type BetweenExpr struct {
	Loc
	Expr Expr
	Low  Expr
	High Expr
}

func (b *BetweenExpr) exprNode() {}

type CaseExpr struct {
	Loc
	Expr     Expr
	WhenList []WhenClause
	Else     Expr
}

type WhenClause struct {
	Loc
	Cond Expr
	Then Expr
}

func (c *CaseExpr) exprNode() {}

type InExpr struct {
	Loc
	Expr     Expr
	List     []Expr
	Subquery Stmt
}

func (i *InExpr) exprNode() {}

type ExistsExpr struct {
	Loc
	Subquery Stmt
}

func (e *ExistsExpr) exprNode() {}

type SubqueryExpr struct {
	Loc
	Subquery Stmt
}

func (s *SubqueryExpr) exprNode() {}

type IntervalLiteral struct {
	Loc
	Value string
	Unit  string
}

func (i *IntervalLiteral) exprNode() {}

type RaiseFunc struct {
	Loc
	Action  string
	Message Expr
}

func (r *RaiseFunc) exprNode() {}

type ColDef struct {
	Name             string
	Type             LX.TokenType
	Size             int
	Precision        int
	Scale            int
	Nullable         bool
	Default          Expr
	PK               bool
	Unique           bool
	Check            Expr
	ReferencesTable  string
	ReferencesColumn string
	OnDelete         string
	OnUpdate         string
	Generated        Expr
	Virtual          bool
	Autoincrement    bool
	Match            string
	Deferrable       string
	Initially        string
}

func NewColDef(name string, typ LX.TokenType) ColDef {
	return ColDef{Name: name, Type: typ, Nullable: true}
}

type Pair struct {
	Col string
	Val Expr
}

type CreateTable struct {
	Loc
	Name              string
	Cols              []ColDef
	PK                *string
	UniqueConstraints []UniqueKey
	ForeignKeys       []ForeignKeyConstraint
	Select            *Select
	WithoutRowid      bool
	Strict            bool
}

type ForeignKeyConstraint struct {
	Columns    []string
	RefTable   string
	RefColumns []string
	OnDelete   string
	OnUpdate   string
	Match      string
	Deferrable string
	Initially  string
}

type UniqueKey struct {
	Cols []string
}

func (u UniqueKey) stmtNode() {}

func UniqueKeyFromName(name string) UniqueKey {
	return UniqueKey{Cols: []string{name}}
}

func (c *CreateTable) stmtNode() {}

type DropTable struct {
	Loc
	Name     string
	IfExists bool
}

func (d *DropTable) stmtNode() {}

type OnConflict struct {
	Columns    []string
	TargetWhere Expr // REQ001364: partial-index WHERE on the conflict target
	DoNothing  bool
	SetClauses []Pair
	UpdateWhere Expr // REQ001365: WHERE on DO UPDATE
}

type ConflictAction int

const (
	ConflictActionUnspecified ConflictAction = iota
	ConflictActionRollback
	ConflictActionAbort
	ConflictActionFail
	ConflictActionIgnore
	ConflictActionReplace
)

type Insert struct {
	Loc
	Table          string
	Cols           []string
	Values         [][]Expr
	Select         Stmt
	Returning      []Expr
	OnConflict     *OnConflict
	ConflictAction ConflictAction
	DefaultValues  bool
}

func (i *Insert) stmtNode() {}

type IndexedColumn struct {
	Name      string
	Collation string
}

type CreateIndexStmt struct {
	Loc
	Name           string
	Table          string
	IndexedColumns []IndexedColumn
	Unique         bool
	IfExists       bool
	Where          Expr
}

func (c *CreateIndexStmt) stmtNode() {}

type DropIndexStmt struct {
	Loc
	Name     string
	IfExists bool
}

func (d *DropIndexStmt) stmtNode() {}

type CommonTableExpr struct {
	Loc
	Name  string
	Cols  []string
	Query Stmt
}

type WithStmt struct {
	Loc
	Recursive bool
	CTEs      []*CommonTableExpr
	Inner     Stmt
}

func (w *WithStmt) stmtNode() {}

type TriggerEvent struct {
	Time  string
	Event string
	Cols  []string
}

type TriggerStmt struct {
	Loc
	Name        string
	Time        string
	Event       string
	OnTable     string
	ForEach     string
	Body        []Stmt
	IfNotExists bool
	When        Expr
	OfCols      []string
}

func (t *TriggerStmt) stmtNode() {}

type SavepointStmt struct {
	Loc
	Name string
}

func (s *SavepointStmt) stmtNode() {}

type ReleaseSavepointStmt struct {
	Loc
	Name string
}

func (r *ReleaseSavepointStmt) stmtNode() {}

type RollbackToStmt struct {
	Loc
	Name string
}

func (r *RollbackToStmt) stmtNode() {}

type IndexHint struct {
	IndexedBy string
}

type Update struct {
	Loc
	Table       string
	Set         []Pair
	Where       Expr
	From        string
	FromAlias   string
	Returning   []Expr
	OrderBy     []OrderItem
	Limit       Expr
	Offset      Expr
	OffsetFirst bool
	IndexHint   *IndexHint
}

func (u *Update) stmtNode() {}

type Delete struct {
	Loc
	Table       string
	Where       Expr
	Returning   []Expr
	OrderBy     []OrderItem
	Limit       Expr
	Offset      Expr
	OffsetFirst bool
	IndexHint   *IndexHint
}

func (d *Delete) stmtNode() {}

type OrderItem struct {
	Loc
	Expr       Expr
	Desc       bool
	Collation  string
	NullsOrder int8
}

type JoinClause struct {
	Loc
	Kind       string
	Right      string
	RightAlias string
	On         Expr
	Using      []string // REQ001361: JOIN .. USING (col1, col2, ...)
	Natural    bool     // REQ001359: NATURAL [INNER] JOIN — implicit ON on common cols
}

type Select struct {
	Loc
	Cols         []Expr
	From         string
	FromAlias    string
	Joins        []JoinClause
	Where        Expr
	OrderBy      []OrderItem
	Limit        Expr
	Offset       Expr
	Distinct     bool
	GroupBy      []Expr
	Having       Expr
	OffsetFirst  bool
	FetchFirst   *FetchFirst
	SubqueryFrom Stmt
	IndexHint    *IndexHint
}

type FetchFirst struct {
	Loc
	Count Expr
}

func (s *Select) stmtNode() {}

type CompoundOp int

const (
	CompoundUnion CompoundOp = iota
	CompoundUnionAll
	CompoundIntersect
	CompoundExcept
)

func (c CompoundOp) String() string {
	switch c {
	case CompoundUnionAll:
		return "UNION ALL"
	case CompoundIntersect:
		return "INTERSECT"
	case CompoundExcept:
		return "EXCEPT"
	}
	return "UNION"
}

type CompoundStmt struct {
	Loc
	Left        Stmt
	Op          CompoundOp
	Right       Stmt
	OrderBy     []OrderItem
	Limit       Expr
	Offset      Expr
	OffsetFirst bool
	FetchFirst  *FetchFirst
}

func (c *CompoundStmt) stmtNode() {}

type BeginTX struct {
	Loc
	Mode string
}

func (b *BeginTX) stmtNode() {}

type CommitTX struct {
	Loc
}

func (c *CommitTX) stmtNode() {}

type RollbackTX struct {
	Loc
}

func (r *RollbackTX) stmtNode() {}

type ExplainMode int

const (
	ExplainNormal ExplainMode = iota
	ExplainQueryPlan
	ExplainAnalyze
)

type ExplainFormat int

const (
	ExplainFormatText ExplainFormat = iota
	ExplainFormatTree
	ExplainFormatJSON
	ExplainFormatDOT
)

type ExplainStmt struct {
	Loc
	Mode   ExplainMode
	Format ExplainFormat
	Inner  Stmt
}

func (e *ExplainStmt) stmtNode() {}

type AnalyzeStmt struct {
	Loc
	Table string
}

func (a *AnalyzeStmt) stmtNode() {}

type VacuumStmt struct {
	Loc
	Table string
}

func (v *VacuumStmt) stmtNode() {}

type CreateViewStmt struct {
	Loc
	Name      string
	As        Stmt
	Temporary bool
}

func (c *CreateViewStmt) stmtNode() {}

type AlterTableStmt struct {
	Loc
	Table    string
	Action   string
	Column   string
	NewCol   *ColDef
	NewName  string
	NewExpr  Expr // REQ001322: SET DEFAULT expression payload
	IfExists bool
}

func (a *AlterTableStmt) stmtNode() {}

type PragmaStmt struct {
	Loc
	Name  string
	Value string
}

func (p *PragmaStmt) stmtNode() {}

type TruncateStmt struct {
	Loc
	Table string
}

func (t *TruncateStmt) stmtNode() {}

type ReindexStmt struct {
	Loc
	Target string
}

func (r *ReindexStmt) stmtNode() {}

type DropViewStmt struct {
	Loc
	Name     string
	IfExists bool
}

func (d *DropViewStmt) stmtNode() {}

type DropTriggerStmt struct {
	Loc
	Name     string
	IfExists bool
}

func (d *DropTriggerStmt) stmtNode() {}

type CreateVirtualTableStmt struct {
	Loc
	Name   string
	Module string
	Args   []string
}

func (c *CreateVirtualTableStmt) stmtNode() {}

type CreateMatViewStmt struct {
	Loc
	Name        string
	As          *Select
	IfNotExists bool
	Incremental bool
	BaseTables  []string
}

func (c *CreateMatViewStmt) stmtNode() {}

type DropMatViewStmt struct {
	Loc
	Name     string
	IfExists bool
}

func (d *DropMatViewStmt) stmtNode() {}

type RefreshMatViewStmt struct {
	Loc
	Name         string
	Concurrently bool
}

func (r *RefreshMatViewStmt) stmtNode() {}

type SetTransactionStmt struct {
	Loc
	Level string
}

func (s *SetTransactionStmt) stmtNode() {}

type ValuesStmt struct {
	Loc
	Rows [][]Expr
}

func (v *ValuesStmt) stmtNode() {}

type AttachStmt struct {
	Loc
	Expr Expr
	Name string
}

func (a *AttachStmt) stmtNode() {}

type DetachStmt struct {
	Loc
	Name string
}

func (d *DetachStmt) stmtNode() {}
