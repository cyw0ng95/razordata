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
	Line     uint32
	Col      uint32
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

func renderCaret(input string, line, col uint32) string {
	l := int(line)
	c := int(col)
	lines := strings.SplitN(input, "\n", l+1)
	if l-1 >= len(lines) {
		return ""
	}
	row := lines[l-1]
	var b strings.Builder
	b.WriteString("    ")
	b.WriteString(row)
	b.WriteByte('\n')
	b.WriteString("    ")
	for i := 1; i < c; i++ {
		b.WriteByte(' ')
	}
	b.WriteString("^\n")
	return b.String()
}
