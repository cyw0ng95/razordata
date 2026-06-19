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
	Op     int
	Left   Expr
	Right  Expr
	Escape Expr // REQ000567: LIKE ... ESCAPE expr
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
	Name      string
	Arg       Expr
	Distinct  bool
	Separator Expr // REQ000523: GROUP_CONCAT optional separator
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
	Name             string
	Type             int
	Size             int
	Precision        int // REQ000568: DECIMAL(P,S) precision
	Scale            int // REQ000568: DECIMAL(P,S) scale
	Nullable         bool
	Default          Expr
	PK               bool
	Unique           bool
	Check            Expr
	ReferencesTable  string // REQ000126: FOREIGN KEY REFERENCES table
	ReferencesColumn string // REQ000126: referenced column
	OnDelete         string // CASCADE, RESTRICT, SET NULL, SET DEFAULT, NO ACTION
	OnUpdate         string // same set
	// REQ000248: generated columns (`AS (expr) STORED`).
	// Expr holds the generation expression; Virtual distinguishes
	// STORED (materialized on write) from VIRTUAL (computed on read).
	// Only STORED is supported in v0.27.0.
	Generated     Expr
	Virtual       bool
	Autoincrement bool   // REQ000482: INTEGER PRIMARY KEY AUTOINCREMENT
	Match         string // REQ000561: MATCH FULL/PARTIAL/SIMPLE
	Deferrable    string // REQ000561: DEFERRABLE / NOT DEFERRABLE
	Initially     string // REQ000561: INITIALLY DEFERRED / IMMEDIATE
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
	ForeignKeys       []ForeignKeyConstraint // REQ000126
	Select            *Select                // non-nil for CREATE TABLE AS SELECT (REQ000520)
}

// ForeignKeyConstraint represents a table-level FOREIGN KEY constraint.
type ForeignKeyConstraint struct {
	Columns    []string // local column names
	RefTable   string   // referenced table
	RefColumns []string // referenced columns
	OnDelete   string   // CASCADE, RESTRICT, SET NULL, SET DEFAULT, NO ACTION
	OnUpdate   string   // same set
	Match      string   // REQ000561: PARTIAL, FULL, SIMPLE
	Deferrable string   // REQ000561: "DEFERRABLE" or "NOT DEFERRABLE"
	Initially  string   // REQ000561: "DEFERRED" or "IMMEDIATE"
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
	Name     string
	IfExists bool // REQ000497: DROP TABLE IF EXISTS
}

func (d *DropTable) stmtNode() {}

// OnConflict represents an ON CONFLICT clause for UPSERT operations.
type OnConflict struct {
	Columns    []string // target columns for conflict detection
	DoNothing  bool     // true = DO NOTHING
	SetClauses []Pair   // DO UPDATE SET clauses
}

// ConflictAction represents INSERT OR <action> / REPLACE conflict resolution.
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
	Table          string
	Cols           []string
	Values         [][]Expr
	Returning      []Expr
	OnConflict     *OnConflict    // nil if no ON CONFLICT clause
	ConflictAction ConflictAction // INSERT OR ROLLBACK/ABORT/FAIL/IGNORE/REPLACE
	DefaultValues  bool           // REQ000563: INSERT INTO t DEFAULT VALUES
}

func (i *Insert) stmtNode() {}

// IndexedColumn represents a column in a CREATE INDEX with optional COLLATE.
type IndexedColumn struct {
	Name      string
	Collation string // REQ000565: COLLATE name
}

// CreateIndexStmt represents a CREATE INDEX statement.
// REQ000251 — secondary indexes MVP.
type CreateIndexStmt struct {
	Name           string          // index name
	Table          string          // target table name
	IndexedColumns []IndexedColumn // REQ000565: columns with optional COLLATE
	Unique         bool            // UNIQUE modifier (reserved; not yet enforced)
	IfExists       bool            // REQ000479: CREATE INDEX IF NOT EXISTS
	Where          Expr            // REQ000566: partial index predicate
}

func (c *CreateIndexStmt) stmtNode() {}

// DropIndexStmt represents a DROP INDEX statement.
// REQ000251 — secondary indexes MVP.
type DropIndexStmt struct {
	Name     string // index name
	IfExists bool   // REQ000480: DROP INDEX IF EXISTS
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
	Recursive bool
	CTEs      []*CommonTableExpr
	Inner     Stmt // the main query
}

func (w *WithStmt) stmtNode() {}

// TriggerEvent is the time and action that fires a trigger.
type TriggerEvent struct {
	Time  string   // "BEFORE" or "AFTER"
	Event string   // "INSERT", "UPDATE", or "DELETE"
	Cols  []string // optional column list for UPDATE OF
}

// TriggerStmt represents a CREATE TRIGGER statement.
// REQ000435.
type TriggerStmt struct {
	Name        string
	Time        string // "BEFORE", "AFTER", or "INSTEAD OF"
	Event       string // "INSERT", "UPDATE", or "DELETE"
	OnTable     string
	ForEach     string // "ROW" or "STATEMENT"
	Body        []Stmt // trigger body statements (BEGIN ... END)
	IfNotExists bool
	When        string   // optional WHEN expression (raw text)
	OfCols      []string // optional column list for UPDATE OF
}

func (t *TriggerStmt) stmtNode() {}

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

// REQ000529/569: IndexHint represents an INDEXED BY name or NOT INDEXED hint.
type IndexHint struct {
	IndexedBy string // non-empty = INDEXED BY name; empty = NOT INDEXED
}

type Update struct {
	Table       string
	Set         []Pair
	Where       Expr
	Returning   []Expr
	OrderBy     []OrderItem // REQ000558
	Limit       Expr        // REQ000558
	Offset      Expr        // REQ000558
	OffsetFirst bool        // REQ000558
	IndexHint   *IndexHint  // REQ000569
}

func (u *Update) stmtNode() {}

type Delete struct {
	Table       string
	Where       Expr
	Returning   []Expr
	OrderBy     []OrderItem
	Limit       Expr
	Offset      Expr
	OffsetFirst bool
	IndexHint   *IndexHint // REQ000569
}

func (d *Delete) stmtNode() {}

type OrderItem struct {
	Expr      Expr
	Desc      bool
	Collation string // REQ000565: COLLATE name
}

type JoinClause struct {
	Kind  string // "INNER", "LEFT", "RIGHT", "CROSS"
	Right string
	On    Expr
}

type Select struct {
	Cols        []Expr
	From        string
	FromAlias   string
	Joins       []JoinClause
	Where       Expr
	OrderBy     []OrderItem
	Limit       Expr
	Offset      Expr
	Distinct    bool
	GroupBy     []Expr
	Having      Expr
	OffsetFirst bool // REQ000521: true when OFFSET appears before LIMIT in the SQL
	// REQ000436 + REQ000084: when FROM is a subquery (e.g. `FROM
	// (SELECT ...)`), SubqueryFrom holds the parsed SELECT and
	// From is set to the alias (or "$$subquery$$" if unnamed).
	// The planner uses this to build a materialized subplan
	// instead of looking up a table by name.
	SubqueryFrom Stmt
	IndexHint    *IndexHint // REQ000529
}

func (s *Select) stmtNode() {}

// CompoundOp encodes the SQL compound-select operator. REQ000383.
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

// CompoundStmt is a `SELECT ... <op> SELECT ...` chain. Left
// and Right may themselves be CompoundStmt (left-associative
// chain), or a plain *Select at the leaves. REQ000383.
type CompoundStmt struct {
	Left  Stmt
	Op    CompoundOp
	Right Stmt
	// OrderBy / Limit / Offset apply to the entire compound result.
	OrderBy     []OrderItem
	Limit       Expr
	Offset      Expr
	OffsetFirst bool // REQ000521: true when OFFSET appears before LIMIT in the SQL
}

func (c *CompoundStmt) stmtNode() {}

type BeginTX struct {
	Mode string // "" for bare BEGIN, "DEFERRED", "IMMEDIATE", "EXCLUSIVE" (REQ000559)
}

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

// CreateViewStmt represents CREATE [TEMP|TEMPORARY] VIEW name AS SELECT ... (REQ000240)
type CreateViewStmt struct {
	Name      string
	As        Stmt // the SELECT statement
	Temporary bool // true if CREATE TEMP/TEMPORARY VIEW
}

func (c *CreateViewStmt) stmtNode() {}

// AlterTableStmt represents ALTER TABLE ... (REQ000243)
type AlterTableStmt struct {
	Table    string
	Action   string  // "ADD COLUMN", "DROP COLUMN", "RENAME", "RENAME COLUMN"
	Column   string  // column name for ADD/DROP/RENAME COLUMN
	NewCol   *ColDef // for ADD COLUMN
	NewName  string  // REQ000498: new name for RENAME COLUMN
	IfExists bool    // for DROP TABLE IF EXISTS (stored for executor)
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

// TruncateStmt represents TRUNCATE [TABLE] name (iter-28 REQ000476)
type TruncateStmt struct {
	Table string
}

func (t *TruncateStmt) stmtNode() {}

// ReindexStmt represents REINDEX [name] (iter-28 REQ000478)
type ReindexStmt struct {
	Target string // empty = reindex all
}

func (r *ReindexStmt) stmtNode() {}

// DropViewStmt represents DROP VIEW [IF EXISTS] name (iter-28 REQ000494)
type DropViewStmt struct {
	Name     string
	IfExists bool
}

func (d *DropViewStmt) stmtNode() {}

// DropTriggerStmt represents DROP TRIGGER [IF EXISTS] name (iter-28 REQ000496)
type DropTriggerStmt struct {
	Name     string
	IfExists bool
}

func (d *DropTriggerStmt) stmtNode() {}

// SetTransactionStmt represents SET TRANSACTION ISOLATION LEVEL ...
type SetTransactionStmt struct {
	Level string // "READ UNCOMMITTED", "READ COMMITTED", "REPEATABLE READ", "SERIALIZABLE"
}

func (s *SetTransactionStmt) stmtNode() {}

func (e *ExplainStmt) stmtNode() {}

// RaiseFunc represents the RAISE() function in triggers (REQ000560).
// RAISE(ABORT, 'error message') causes the trigger to abort with
// the given error message. The single-argument form RAISE(IGNORE)
// suppresses the trigger action.
type RaiseFunc struct {
	Action  string // "ABORT", "IGNORE"
	Message Expr   // nil for RAISE(IGNORE)
}

func (r *RaiseFunc) exprNode() {}

// ValuesStmt represents a standalone VALUES statement (REQ000564).
// Each element of Rows is a row of scalar expressions.
type ValuesStmt struct {
	Rows [][]Expr
}

func (v *ValuesStmt) stmtNode() {}

// AttachStmt represents `ATTACH DATABASE expr AS name` (REQ000557).
// In v1 the executor rejects this statement at runtime with
// "multi-database not supported in v1"; the parser still
// accepts it so applications using SQLite-style multi-db
// idioms get a parse-time success.
type AttachStmt struct {
	Expr Expr   // path expression (typically a string literal)
	Name string // schema alias
}

func (a *AttachStmt) stmtNode() {}

// DetachStmt represents `DETACH DATABASE name` (REQ000557).
type DetachStmt struct {
	Name string // schema alias
}

func (d *DetachStmt) stmtNode() {}
