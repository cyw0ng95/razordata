package sqlcmp

import (
	"testing"
)

type workflowCase struct {
	name  string
	stmts []string
}

var workflowCases = []workflowCase{
	{
		name: "users_crud",
		stmts: []string{
			"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT, age INTEGER)",
			"INSERT INTO users VALUES (1, 'alice', 'alice@example.com', 30)",
			"INSERT INTO users VALUES (2, 'bob', 'bob@example.com', 25)",
			"INSERT INTO users VALUES (3, 'charlie', 'charlie@example.com', 35)",
			"SELECT * FROM users WHERE age > 25 ORDER BY age",
			"SELECT name, email FROM users WHERE name = 'alice'",
			"SELECT * FROM users ORDER BY name LIMIT 2 OFFSET 1",
			"UPDATE users SET age = 31 WHERE name = 'alice'",
			"SELECT * FROM users WHERE age = 31",
			"DELETE FROM users WHERE name = 'charlie'",
			"SELECT * FROM users",
		},
	},
	{
		name: "orders_workflow",
		stmts: []string{
			"CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER, product TEXT, amount REAL)",
			"INSERT INTO orders VALUES (1, 1, 'widget', 19.99)",
			"INSERT INTO orders VALUES (2, 1, 'gadget', 29.99)",
			"INSERT INTO orders VALUES (3, 2, 'widget', 19.99)",
			"SELECT * FROM orders WHERE amount > 20 ORDER BY amount DESC",
			"UPDATE orders SET amount = 24.99 WHERE id = 1",
			"SELECT * FROM orders WHERE id = 1",
		},
	},
	{
		name: "numbers_filter",
		stmts: []string{
			"CREATE TABLE numbers (n INTEGER)",
			"INSERT INTO numbers VALUES (1)",
			"INSERT INTO numbers VALUES (2)",
			"INSERT INTO numbers VALUES (3)",
			"INSERT INTO numbers VALUES (4)",
			"INSERT INTO numbers VALUES (5)",
			"SELECT * FROM numbers WHERE n > 2 AND n < 5",
			"SELECT * FROM numbers WHERE n IN (1, 3, 5)",
			"SELECT * FROM numbers WHERE n BETWEEN 2 AND 4",
		},
	},
	{
		name: "products_inventory",
		stmts: []string{
			"CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT, qty INTEGER, price REAL)",
			"INSERT INTO products VALUES (1, 'apple', 100, 0.50)",
			"INSERT INTO products VALUES (2, 'banana', 50, 0.30)",
			"INSERT INTO products VALUES (3, 'orange', 75, 0.60)",
			"SELECT * FROM products WHERE qty < 100 ORDER BY name",
			"UPDATE products SET qty = qty - 10 WHERE id = 1",
			"SELECT * FROM products WHERE id = 1",
		},
	},
}

func TestWorkflowParse(t *testing.T) {
	for _, wf := range workflowCases {
		t.Run(wf.name, func(t *testing.T) {
			for i, sql := range wf.stmts {
				p := NewParser(sql)
				_, err := p.Parse()
				if err != nil {
					t.Errorf("workflow %s step %d: parse error for %q: %v", wf.name, i, sql, err)
				}
			}
		})
	}
}

func TestWorkflowRewrite(t *testing.T) {
	for _, wf := range workflowCases {
		t.Run(wf.name, func(t *testing.T) {
			for i, sql := range wf.stmts {
				p := NewParser(sql)
				stmt, err := p.Parse()
				if err != nil {
					t.Errorf("workflow %s step %d: parse error for %q: %v", wf.name, i, sql, err)
					continue
				}
				_, err = Rewrite(stmt)
				if err != nil {
					t.Errorf("workflow %s step %d: rewrite error for %q: %v", wf.name, i, sql, err)
				}
			}
		})
	}
}

func TestWorkflowSQLite(t *testing.T) {
	if SkipSQLite() {
		t.Skip("SQLite not available")
	}
	for _, wf := range workflowCases {
		t.Run(wf.name, func(t *testing.T) {
			r := NewRunner()
			for i, sql := range wf.stmts {
				_, err := r.CompareQuery(sql)
				if err != nil {
					t.Errorf("workflow %s step %d: SQLite error for %q: %v", wf.name, i, sql, err)
				}
			}
		})
	}
}
