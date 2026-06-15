package EX

import (
	"testing"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

func TestCoreFunctions_Eval(t *testing.T) {
	tests := []struct {
		sql  string
		want interface{}
	}{
		{"SELECT CHAR(65, 66, 67)", "ABC"},
		{"SELECT CONCAT('Hello', ' ', 'World')", "Hello World"},
		{"SELECT CONCAT_WS(',', 'a', 'b', 'c')", "a,b,c"},
		{"SELECT FORMAT('Hello %s', 'World')", "Hello World"},
		{"SELECT LTRIM('  hello  ')", "hello  "},
		{"SELECT RTRIM('  hello  ')", "  hello"},
		{"SELECT REPLACE('aaa', 'a', 'b')", "bbb"},
		{"SELECT QUOTE('it''s')", "'it''s'"},
		{"SELECT TYPEOF(42)", "integer"},
		{"SELECT TYPEOF('text')", "text"},
		{"SELECT TYPEOF(NULL)", "null"},
		{"SELECT OCTET_LENGTH('café')", int64(5)},
		{"SELECT UNICODE('A')", int64(65)},
		{"SELECT SQLITE_VERSION()", "0.26.7"},
		{"SELECT SQLITE_SOURCE_ID()", "razordata-v0.26.7"},
		{"SELECT IIF(1 > 0, 'yes', 'no')", "yes"},
		{"SELECT INSTR('hello world', 'world')", int64(7)},
		{"SELECT SIGN(-5)", int64(-1)},
		{"SELECT SIGN(0)", int64(0)},
		{"SELECT SIGN(5)", int64(1)},
	}
	
	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			parser := PS.NewParser(tt.sql)
			stmt, err := parser.Parse()
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			
			sel, ok := stmt.(*PS.Select)
			if !ok {
				t.Fatalf("Not a SELECT: %T", stmt)
			}
			if len(sel.Cols) != 1 {
				t.Fatalf("Expected 1 column, got %d", len(sel.Cols))
			}
			
			got, err := Eval(sel.Cols[0], nil, nil)
			if err != nil {
				t.Fatalf("Eval error: %v", err)
			}
			
			switch want := tt.want.(type) {
			case int64:
				if gb, ok := got.(int64); !ok || gb != want {
					t.Fatalf("Got %v (%T), want %v", got, got, want)
				}
			case string:
				if gb, ok := got.(string); !ok || gb != want {
					t.Fatalf("Got %v (%T), want %v", got, got, want)
				}
			default:
				if got != want {
					t.Fatalf("Got %v (%T), want %v (%T)", got, got, want, want)
				}
			}
		})
	}
}

func TestZeroblob_Eval(t *testing.T) {
	parser := PS.NewParser("SELECT ZEROBLOB(8)")
	stmt, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	sel := stmt.(*PS.Select)
	got, err := Eval(sel.Cols[0], nil, nil)
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	buf, ok := got.([]byte)
	if !ok {
		t.Fatalf("Got %T, want []byte", got)
	}
	if len(buf) != 8 {
		t.Fatalf("Got len=%d, want 8", len(buf))
	}
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("buf[%d]=%d, want 0", i, b)
		}
	}
}

func TestCoreFunctions_Batch2(t *testing.T) {
	tests := []struct {
		sql  string
		want interface{}
	}{
		// glob
		{"SELECT GLOB('*.txt', 'hello.txt')", int64(1)},
		{"SELECT GLOB('*.txt', 'hello.doc')", int64(0)},
		{"SELECT GLOB('file?.txt', 'file1.txt')", int64(1)},
		// soundex
		{"SELECT SOUNDEX('Robert')", "R163"},
		{"SELECT SOUNDEX('Rupert')", "R163"},
		{"SELECT SOUNDEX('Andrew')", "A536"},
		{"SELECT SOUNDEX(NULL)", nil},
		// unhex
		{"SELECT UNHEX('48656c6c6f')", []byte("Hello")},
		{"SELECT UNHEX('')", []byte{}},
		{"SELECT UNHEX(NULL)", nil},
		// unistr
		{"SELECT UNISTR('\\u0041\\u0042')", "AB"},
		{"SELECT UNISTR('\\n')", "\n"},
		{"SELECT UNISTR('\\\\')", "\\"},
		// likelihood/likely/unlikely (no-op)
		{"SELECT LIKELIHOOD(42, 0.5)", int64(42)},
		{"SELECT LIKELY(100)", int64(100)},
		{"SELECT UNLIKELY('text')", "text"},
	}
	
	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			parser := PS.NewParser(tt.sql)
			stmt, err := parser.Parse()
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			
			sel, ok := stmt.(*PS.Select)
			if !ok {
				t.Fatalf("Not a SELECT: %T", stmt)
			}
			if len(sel.Cols) != 1 {
				t.Fatalf("Expected 1 column, got %d", len(sel.Cols))
			}
			
			got, err := Eval(sel.Cols[0], nil, nil)
			if err != nil {
				t.Fatalf("Eval error: %v", err)
			}
			
			switch want := tt.want.(type) {
			case int64:
				if gb, ok := got.(int64); !ok || gb != want {
					t.Fatalf("Got %v (%T), want %v", got, got, want)
				}
			case string:
				if gb, ok := got.(string); !ok || gb != want {
					t.Fatalf("Got %v (%T), want %v", got, got, want)
				}
			case []byte:
				if gb, ok := got.([]byte); !ok {
					t.Fatalf("Got %T, want []byte", got)
				} else if string(gb) != string(want) {
					t.Fatalf("Got %v, want %v", gb, want)
				}
			default:
				if got != want {
					t.Fatalf("Got %v (%T), want %v (%T)", got, got, want, want)
				}
			}
		})
	}
}
