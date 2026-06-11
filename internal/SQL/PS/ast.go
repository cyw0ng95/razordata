package PS

type Expr interface {
	exprNode()
}

type Stmt interface {
	stmtNode()
}

type NumberLiteral struct {
	Val int64
}

func (n *NumberLiteral) exprNode() {}

type FloatLiteral struct {
	Val float64
}

func (f *FloatLiteral) exprNode() {}

type StringLiteral struct {
	Val string
}

func (s *StringLiteral) exprNode() {}

type BoolLiteral struct {
	Val bool
}

func (b *BoolLiteral) exprNode() {}

type NullLiteral struct{}

func (n *NullLiteral) exprNode() {}

type Ident struct {
	Name string
}

func (i *Ident) exprNode() {}

type QualifiedName struct {
	Table string
	Name  string
}

func (q *QualifiedName) exprNode() {}

type AliasedExpr struct {
	Expr  Expr
	Alias string
}

func (a *AliasedExpr) exprNode() {}

type CastExpr struct {
	Expr Expr
	Type *TypeInfo
}

func (c *CastExpr) exprNode() {}

type Param struct {
	Index int
}

func (p *Param) exprNode() {}

type BinaryExpr struct {
	Op    int
	Left  Expr
	Right Expr
}

func (b *BinaryExpr) exprNode() {}

type UnaryExpr struct {
	Op      int
	Operand Expr
}

func (u *UnaryExpr) exprNode() {}

type FunctionCall struct {
	Name string
	Args []Expr
}

func (f *FunctionCall) exprNode() {}

type AggregateFunc struct {
	Name string
	Arg  Expr
}

func (a *AggregateFunc) exprNode() {}

// WindowSpec represents the OVER clause of a window function.
type WindowSpec struct {
	PartitionBy []Expr
	OrderBy     []OrderItem
	Frame       *WindowFrame
}

// WindowFrame represents ROWS/RANGE frame specification.
type WindowFrame struct {
	Type  string // "ROWS" or "RANGE"
	Start FrameBound
	End   FrameBound
}

// FrameBound represents a frame boundary.
type FrameBound struct {
	Type   string // "UNBOUNDED_PRECEDING", "PRECEDING", "CURRENT_ROW", "FOLLOWING", "UNBOUNDED_FOLLOWING"
	Offset Expr   // offset for PRECEDING/FOLLOWING (nil for UNBOUNDED/CURRENT)
}

// WindowFunc represents a window function call with OVER clause.
type WindowFunc struct {
	Name string // ROW_NUMBER, RANK, DENSE_RANK, LAG, LEAD, etc.
	Args []Expr
	Over *WindowSpec
}

func (w *WindowFunc) exprNode() {}

type StarExpr struct{}

func (s *StarExpr) exprNode() {}

type ListExpr struct {
	Items []Expr
}

func (l *ListExpr) exprNode() {}

type BetweenExpr struct {
	Expr Expr
	Low  Expr
	High Expr
}

func (b *BetweenExpr) exprNode() {}

type CaseExpr struct {
	Expr     Expr
	WhenList []WhenClause
	Else     Expr
}

type WhenClause struct {
	Cond Expr
	Then Expr
}

func (c *CaseExpr) exprNode() {}

type InExpr struct {
	Expr     Expr
	List     []Expr
	Subquery Stmt
}

func (i *InExpr) exprNode() {}

type ExistsExpr struct {
	Subquery Stmt
}

func (e *ExistsExpr) exprNode() {}

type SubqueryExpr struct {
	Subquery Stmt
}

func (s *SubqueryExpr) exprNode() {}

// IntervalLiteral represents an INTERVAL expression like INTERVAL '7' DAY.
type IntervalLiteral struct {
	Value string // the numeric part as string, e.g. "7"
	Unit  string // YEAR, MONTH, DAY, HOUR, MINUTE, SECOND
}

func (i *IntervalLiteral) exprNode() {}

type ColDef struct {
	Name     string
	Type     int
	Size     int
	Nullable bool
	Default  Expr
	PK       bool
	Unique   bool
	Check    Expr
}

func NewColDef(name string, typ int) ColDef {
	return ColDef{Name: name, Type: typ, Nullable: true}
}

type Pair struct {
	Col string
	Val Expr
}

type CreateTable struct {
	Name              string
	Cols              []ColDef
	PK                *string
	UniqueConstraints []UniqueKey
}

// UniqueKey represents a UNIQUE constraint over one or more columns.
// Cols holds column names as written in the SQL (resolved to indices
// at registration time by the executor).
type UniqueKey struct {
	Cols []string
}

func (u UniqueKey) stmtNode() {}

// UniqueKeyFromName constructs a single-column UniqueKey.
func UniqueKeyFromName(name string) UniqueKey {
	return UniqueKey{Cols: []string{name}}
}

func (c *CreateTable) stmtNode() {}

type DropTable struct {
	Name string
}

func (d *DropTable) stmtNode() {}

// OnConflict represents an ON CONFLICT clause for UPSERT operations.
type OnConflict struct {
	Columns    []string // target columns for conflict detection
	DoNothing  bool     // true = DO NOTHING
	SetClauses []Pair   // DO UPDATE SET clauses
}

type Insert struct {
	Table      string
	Cols       []string
	Values     [][]Expr
	Returning  []Expr
	OnConflict *OnConflict // nil if no ON CONFLICT clause
}

func (i *Insert) stmtNode() {}

// CreateIndexStmt represents a CREATE INDEX statement.
// REQ000251 — secondary indexes MVP.
type CreateIndexStmt struct {
	Name    string   // index name
	Table   string   // target table name
	Columns []string // indexed column names
	Unique  bool     // UNIQUE modifier (reserved; not yet enforced)
}

func (c *CreateIndexStmt) stmtNode() {}

// DropIndexStmt represents a DROP INDEX statement.
// REQ000251 — secondary indexes MVP.
type DropIndexStmt struct {
	Name string // index name
}

func (d *DropIndexStmt) stmtNode() {}

// CommonTableExpr represents a CTE (Common Table Expression) definition.
type CommonTableExpr struct {
	Name  string   // CTE name
	Cols  []string // optional column aliases
	Query Stmt     // SELECT statement
}

// WithStmt represents a WITH clause containing CTEs.
type WithStmt struct {
	CTEs  []*CommonTableExpr
	Inner Stmt // the main query
}

func (w *WithStmt) stmtNode() {}

// SavepointStmt represents a SAVEPOINT statement.
type SavepointStmt struct {
	Name string
}

func (s *SavepointStmt) stmtNode() {}

// ReleaseSavepointStmt represents a RELEASE SAVEPOINT statement.
type ReleaseSavepointStmt struct {
	Name string
}

func (r *ReleaseSavepointStmt) stmtNode() {}

// RollbackToStmt represents a ROLLBACK TO SAVEPOINT statement.
type RollbackToStmt struct {
	Name string
}

func (r *RollbackToStmt) stmtNode() {}

type Update struct {
	Table     string
	Set       []Pair
	Where     Expr
	Returning []Expr
}

func (u *Update) stmtNode() {}

type Delete struct {
	Table     string
	Where     Expr
	Returning []Expr
}

func (d *Delete) stmtNode() {}

type OrderItem struct {
	Expr Expr
	Desc bool
}

type JoinClause struct {
	Kind  string // "INNER", "LEFT", "RIGHT", "CROSS"
	Right string
	On    Expr
}

type Select struct {
	Cols      []Expr
	From      string
	FromAlias string
	Joins     []JoinClause
	Where     Expr
	OrderBy   []OrderItem
	Limit     Expr
	Offset    Expr
	Distinct  bool
	GroupBy   []Expr
	Having    Expr
}

func (s *Select) stmtNode() {}

type BeginTX struct{}

func (b *BeginTX) stmtNode() {}

type CommitTX struct{}

func (c *CommitTX) stmtNode() {}

type RollbackTX struct{}

func (r *RollbackTX) stmtNode() {}

type ExplainMode int

const (
	ExplainNormal ExplainMode = iota
	ExplainQueryPlan
)

type ExplainStmt struct {
	Mode  ExplainMode
	Inner Stmt
}

// AnalyzeStmt represents ANALYZE [table_name]
type AnalyzeStmt struct {
	Table string // empty = analyze all tables
}

// VacuumStmt represents VACUUM [table_name]
type VacuumStmt struct {
	Table string // empty = vacuum all tables
}

// CreateViewStmt represents CREATE VIEW name AS SELECT ... (REQ000240)
type CreateViewStmt struct {
	Name string
	As   Stmt // the SELECT statement
}

func (c *CreateViewStmt) stmtNode() {}

// AlterTableStmt represents ALTER TABLE ... (REQ000243)
type AlterTableStmt struct {
	Table  string
	Action string // "ADD COLUMN", "DROP COLUMN", "RENAME"
	Column string // column name for ADD/DROP
	NewCol *ColDef // for ADD COLUMN
}

func (a *AlterTableStmt) stmtNode() {}

// PragmaStmt represents PRAGMA name [= value]
type PragmaStmt struct {
	Name  string
	Value string // optional, empty for read-only pragmas
}

func (a *AnalyzeStmt) stmtNode() {}

func (v *VacuumStmt) stmtNode() {}

func (p *PragmaStmt) stmtNode() {}

// SetTransactionStmt represents SET TRANSACTION ISOLATION LEVEL ...
type SetTransactionStmt struct {
	Level string // "READ UNCOMMITTED", "READ COMMITTED", "REPEATABLE READ", "SERIALIZABLE"
}

func (s *SetTransactionStmt) stmtNode() {}


func (e *ExplainStmt) stmtNode() {}
