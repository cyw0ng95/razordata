package sqlcmp

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
	"github.com/cyw0ng95/razordata/internal/SQL/RE"
)

type Runner struct {
	sqlitePath string
}

func NewRunner() *Runner {
	path, err := exec.LookPath("sqlite3")
	if err != nil {
		return &Runner{sqlitePath: ""}
	}
	return &Runner{sqlitePath: path}
}

func (r *Runner) Compare(input string) error {
	p := PS.NewParser(input)
	stmt, err := p.Parse()
	if err != nil {
		return err
	}
	_, err = RE.Format(stmt)
	return err
}

func (r *Runner) CompareParse(input string) (PS.Stmt, error) {
	p := PS.NewParser(input)
	return p.Parse()
}

func (r *Runner) CompareDML(input string) (int64, error) {
	if r.sqlitePath == "" {
		return 0, nil
	}
	cmd := exec.Command("sqlite3", ":memory:")
	cmd.Stdin = strings.NewReader(input + "; SELECT changes();")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	return 0, nil
}

func (r *Runner) CompareQuery(input string) ([]string, error) {
	if r.sqlitePath == "" {
		return nil, nil
	}
	cmd := exec.Command("sqlite3", ":memory:")
	cmd.Stdin = strings.NewReader(input)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	return lines, nil
}

func (r *Runner) CompareWorkflow(stmts []string) error {
	if r.sqlitePath == "" {
		return nil
	}
	cmd := exec.Command("sqlite3", ":memory:")
	cmd.Stdin = strings.NewReader(strings.Join(stmts, ";\n") + ";\n")
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v: %s", err, stderr.String())
	}
	return nil
}

func (r *Runner) SkipSQLite() bool {
	return r.sqlitePath == ""
}

func SkipSQLite() bool {
	_, err := exec.LookPath("sqlite3")
	return err != nil
}

func Tokenize(input string) []LX.Token {
	lex := LX.NewLexer(input)
	var tokens []LX.Token
	for {
		token := lex.Next()
		tokens = append(tokens, token)
		if token.Type == LX.T_EOF {
			break
		}
	}
	return tokens
}

func NewLexer(input string) *LX.Lexer {
	return LX.NewLexer(input)
}

func NewParser(input string) *PS.Parser {
	return PS.NewParser(input)
}

func Rewrite(stmt PS.Stmt) (string, error) {
	return RE.Format(stmt)
}

type Token = LX.Token

const (
	T_EOF = LX.T_EOF
)
