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

type ColDef struct {
	Name     string
	Type     int
	Size     int
	Nullable bool
	Default  Expr
	PK       bool
	Unique   bool
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

type Insert struct {
	Table  string
	Cols   []string
	Values [][]Expr
}

func (i *Insert) stmtNode() {}

type Update struct {
	Table string
	Set   []Pair
	Where Expr
}

func (u *Update) stmtNode() {}

type Delete struct {
	Table string
	Where Expr
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
