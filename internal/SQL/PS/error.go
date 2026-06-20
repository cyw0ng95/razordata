package PS

import (
	"errors"
	"fmt"
	"strings"
)

// ErrSyntax is the sentinel error for all syntax errors.
var ErrSyntax = errors.New("ps: syntax error")

// SyntaxError represents a SQL syntax error with location information.
type SyntaxError struct {
	Input    string
	Line     int
	Col      int
	Expected string
	Got      string
	Lexeme   string
}

func (e *SyntaxError) Error() string {
	expected := e.Expected
	if expected == "" {
		expected = "expression"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ps: syntax error at line %d col %d: expected %s, got %s",
		e.Line, e.Col, expected, e.Got)
	if e.Lexeme != "" && e.Got != e.Lexeme {
		fmt.Fprintf(&b, " (%q)", e.Lexeme)
	}
	b.WriteByte('\n')
	b.WriteString(renderCaret(e.Input, e.Line, e.Col))
	return b.String()
}

func (e *SyntaxError) Unwrap() error {
	return ErrSyntax
}

func renderCaret(input string, line, col int) string {
	lines := strings.SplitN(input, "\n", line+1)
	if line-1 >= len(lines) {
		return ""
	}
	row := lines[line-1]
	var b strings.Builder
	b.WriteString("    ")
	b.WriteString(row)
	b.WriteByte('\n')
	b.WriteString("    ")
	for i := 1; i < col; i++ {
		b.WriteByte(' ')
	}
	b.WriteString("^\n")
	return b.String()
}
