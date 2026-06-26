package PS

import "testing"

func BenchmarkParseSelectStar(b *testing.B) {
	for i := 0; i < b.N; i++ {
		p := NewParser("SELECT * FROM users")
		_, err := p.Parse()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseSelectWithFilter(b *testing.B) {
	for i := 0; i < b.N; i++ {
		p := NewParser("SELECT id, name, age FROM users WHERE age > 30 AND age < 50 ORDER BY age LIMIT 100")
		_, err := p.Parse()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseInsert(b *testing.B) {
	for i := 0; i < b.N; i++ {
		p := NewParser("INSERT INTO t (a, b, c) VALUES (1, 'x', 3.14)")
		_, err := p.Parse()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseCreateTable(b *testing.B) {
	for i := 0; i < b.N; i++ {
		p := NewParser("CREATE TABLE users (id INTEGER PRIMARY KEY, name VARCHAR NOT NULL, age INTEGER, email TEXT UNIQUE)")
		_, err := p.Parse()
		if err != nil {
			b.Fatal(err)
		}
	}
}
