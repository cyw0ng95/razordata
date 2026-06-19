package PS

import (
	"fmt"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

type Parser struct {
	lex                  *LX.Lexer
	current              LX.Token
	paramIndex           int
	pendingJoins         []string // REQ000368: comma-separated tables awaiting CROSS-join synthesis
	pendingSubquery      Stmt     // REQ000436: subquery from FROM clause
	pendingSubqueryAlias string
}

func NewParser(input string) *Parser {
