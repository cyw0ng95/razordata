package PS

// Expr is the interface for all expression AST nodes.
type Expr interface {
	exprNode()
}

// Stmt is the interface for all statement AST nodes.
type Stmt interface {
	stmtNode()
}

// NumberLiteral represents an integer literal value.
type NumberLiteral struct {
	Val int64
}

func (n *NumberLiteral) exprNode() {}

// FloatLiteral represents a floating-point literal value.
type FloatLiteral struct {
	Val float64
}

func (f *FloatLiteral) exprNode() {}

// StringLiteral represents a string literal value.
type StringLiteral struct {
	Val string
}

func (s *StringLiteral) exprNode() {}

// BoolLiteral represents a boolean literal value (TRUE/FALSE).
type BoolLiteral struct {
	Val bool
}

func (b *BoolLiteral) exprNode() {}

// NullLiteral represents a NULL value.
type NullLiteral struct{}

func (n *NullLiteral) exprNode() {}

// Ident represents an unqualified identifier (column or table name).
type Ident struct {
	Name string
}

func (i *Ident) exprNode() {}

// QualifiedName represents a qualified identifier (table.column).
type QualifiedName struct {
	Table string
	Name  string
	// CachedKey is "Table.Name" computed once on first use. Lazy
	// init — safe because the expression tree is read-only after
	// parsing and Eval runs single-threaded per benchmark.
	CachedKey string
}

func (q *QualifiedName) exprNode() {}

// AliasedExpr represents an expression with an alias (expr AS alias).
type AliasedExpr struct {
	Expr  Expr
	Alias string
}

func (a *AliasedExpr) exprNode() {}

// CastExpr represents a CAST expression (CAST(expr AS type)).
type CastExpr struct {
	Expr Expr
	Type *TypeInfo
}

func (c *CastExpr) exprNode() {}

// Param represents a bind parameter placeholder (?).
type Param struct {
	Index int
}

func (p *Param) exprNode() {}

// BinaryExpr represents a binary operation (a OP b).
type BinaryExpr struct {
	Op     int
	Left   Expr
	Right  Expr
	Escape Expr // REQ000567: LIKE ... ESCAPE expr
}

func (b *BinaryExpr) exprNode() {}

// UnaryExpr represents a unary operation (OP a).
type UnaryExpr struct {
	Op      int
	Operand Expr
}

func (u *UnaryExpr) exprNode() {}

// FunctionCall represents a scalar function invocation.
type FunctionCall struct {
	Name string
	Args []Expr
}

func (f *FunctionCall) exprNode() {}

// AggregateFunc represents an aggregate function (COUNT, SUM, etc.).
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

// StarExpr represents the * wildcard in SELECT.
type StarExpr struct{}

func (s *StarExpr) exprNode() {}

// ListExpr represents a list of expressions (e.g., IN list).
type ListExpr struct {
	Items []Expr
}

func (l *ListExpr) exprNode() {}

// BetweenExpr represents a BETWEEN expression (expr BETWEEN low AND high).
type BetweenExpr struct {
	Expr Expr
	Low  Expr
	High Expr
}

func (b *BetweenExpr) exprNode() {}

// CaseExpr represents a CASE expression (simple or searched).
type CaseExpr struct {
	Expr     Expr
	WhenList []WhenClause
	Else     Expr
}

// WhenClause represents a WHEN condition THEN result pair.
type WhenClause struct {
	Cond Expr
	Then Expr
}

func (c *CaseExpr) exprNode() {}

// InExpr represents an IN expression (expr IN (list) or expr IN (subquery)).
type InExpr struct {
	Expr     Expr
	List     []Expr
	Subquery Stmt
}

func (i *InExpr) exprNode() {}

// ExistsExpr represents an EXISTS subquery expression.
type ExistsExpr struct {
	Subquery Stmt
}

func (e *ExistsExpr) exprNode() {}

// SubqueryExpr represents a scalar subquery expression.
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

// ColDef represents a column definition in CREATE TABLE.
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

// NewColDef creates a new column definition with the given name and type.
func NewColDef(name string, typ int) ColDef {
	return ColDef{Name: name, Type: typ, Nullable: true}
}

// Pair represents a column-value pair used in UPDATE SET clauses.
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
	WithoutRowid      bool                   // REQ000738
	Strict            bool                   // REQ000739
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

// DropTable represents a DROP TABLE statement.
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

// Insert represents an INSERT statement.
type Insert struct {
	Table          string
	Cols           []string
	Values         [][]Expr
	Select         Stmt // REQ000707: INSERT INTO t SELECT ...
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
	When        Expr     // REQ000741: parsed WHEN expression (nil if absent)
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

// IndexHint represents an INDEXED BY name or NOT INDEXED hint.
type IndexHint struct {
	IndexedBy string // non-empty = INDEXED BY name; empty = NOT INDEXED
}

// Update represents an UPDATE statement.
type Update struct {
	Table       string
	Set         []Pair
	Where       Expr
	From        string // REQ000740: optional FROM table in UPDATE ... FROM
	FromAlias   string // REQ000740: alias for the FROM table
	Returning   []Expr
	OrderBy     []OrderItem // REQ000558
	Limit       Expr        // REQ000558
	Offset      Expr        // REQ000558
	OffsetFirst bool        // REQ000558
	IndexHint   *IndexHint  // REQ000569
}

func (u *Update) stmtNode() {}

// Delete represents a DELETE statement.
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

// OrderItem represents an ORDER BY clause item.
type OrderItem struct {
	Expr      Expr
	Desc      bool
	Collation string // REQ000565: COLLATE name
	// REQ000736: null ordering. 0=not specified, 1=NULLS FIRST,
	// -1=NULLS LAST.
	NullsOrder int8
}

// JoinClause represents a JOIN clause.
type JoinClause struct {
	Kind       string // "INNER", "LEFT", "RIGHT", "CROSS"
	Right      string
	RightAlias string
	On         Expr
}

// Select represents a SELECT statement.
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

// String returns the SQL keyword for the compound operator.
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

// BeginTX represents a BEGIN TRANSACTION statement.
type BeginTX struct {
	Mode string // "" for bare BEGIN, "DEFERRED", "IMMEDIATE", "EXCLUSIVE" (REQ000559)
}

func (b *BeginTX) stmtNode() {}

// CommitTX represents a COMMIT statement.
type CommitTX struct{}

func (c *CommitTX) stmtNode() {}

// RollbackTX represents a ROLLBACK statement.
type RollbackTX struct{}

func (r *RollbackTX) stmtNode() {}

// ExplainMode represents the EXPLAIN mode.
type ExplainMode int

const (
	ExplainNormal ExplainMode = iota
	ExplainQueryPlan
	ExplainAnalyze
)

// ExplainStmt represents an EXPLAIN statement.
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

// CreateVirtualTableStmt represents CREATE VIRTUAL TABLE ... USING module(args).
type CreateVirtualTableStmt struct {
	Name   string
	Module string
	Args   []string
}

func (c *CreateVirtualTableStmt) stmtNode() {}

// CreateMatViewStmt represents CREATE MATERIALIZED VIEW name AS SELECT ...
// For incremental matviews, base tables are tracked and triggers fire on changes.
type CreateMatViewStmt struct {
	Name        string
	As          *Select
	IfNotExists bool
	Incremental bool     // true = auto-maintained via triggers; false = manual REFRESH
	BaseTables  []string // tables referenced in the SELECT (populated at exec)
}

func (c *CreateMatViewStmt) stmtNode() {}

// DropMatViewStmt represents DROP MATERIALIZED VIEW [IF EXISTS] name.
type DropMatViewStmt struct {
	Name     string
	IfExists bool
}

func (d *DropMatViewStmt) stmtNode() {}

// RefreshMatViewStmt represents REFRESH MATERIALIZED VIEW [CONCURRENTLY] name.
type RefreshMatViewStmt struct {
	Name         string
	Concurrently bool // not yet implemented; accepted for syntax compatibility
}

func (r *RefreshMatViewStmt) stmtNode() {}

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
