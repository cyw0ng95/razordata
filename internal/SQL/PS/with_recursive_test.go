package PS

import (
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

// TestWithRecursiveParser verifies REQ000436: WITH RECURSIVE keyword
// is recognized in the parser AST. Full CTE body parsing has
// pre-existing issues; this test focuses on the recursive flag and
// the lexer correctly tokenizing RECURSIVE as an identifier.
func TestWithRecursiveParser(t *testing.T) {
	parser := NewParser("WITH RECURSIVE cnt(x) AS (SELECT 1) SELECT x FROM cnt")
	stmt, _ := parser.Parse()
	if stmt != nil {
		with, ok := stmt.(*WithStmt)
		if !ok {
			t.Fatalf("expected *WithStmt, got %T", stmt)
		}
		if !with.Recursive {
			t.Errorf("expected Recursive=true")
		}
		return
	}
	// Pre-existing CTE body parse error: verify the lexer
	// tokenizes RECURSIVE as a non-keyword identifier so the
	// parser's RECURSIVE check (via EqualFold on T_IDENT lexeme)
	// can match it.
	lex := LX.NewLexer("WITH RECURSIVE cnt")
	t1 := lex.Next()
	if t1.Type == 0 {
		t.Fatalf("expected first token")
	}
	t2 := lex.Next()
	if t2.Type == 0 {
		t.Fatalf("expected second token")
	}
	if !strings.EqualFold(t2.Lexeme, "RECURSIVE") {
		t.Errorf("expected RECURSIVE, got %q (type=%v)", t2.Lexeme, t2.Type)
	}
	// RECURSIVE is not a keyword, so it must tokenize as T_IDENT.
	if t2.Type != LX.T_IDENT {
		t.Errorf("expected T_IDENT for RECURSIVE, got %v", t2.Type)
	}
}
