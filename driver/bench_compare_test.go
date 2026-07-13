package driver

import (
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

const benchRows = 100

// Razordata helpers

func razorOpen(b *testing.B) *sql.DB {
	b.Helper()
	db, err := sql.Open("razor", ":memory:")
	if err != nil {
		b.Fatalf("sql.Open razor: %v", err)
	}
	return db
}

func razorSetup(b *testing.B, db *sql.DB) {
	b.Helper()
	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT, age INTEGER, score REAL)"); err != nil {
		b.Fatalf("CREATE: %v", err)
	}
}

// modernc helpers

func sqliteOpen(b *testing.B) *sql.DB {
	b.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatalf("sql.Open sqlite: %v", err)
	}
	return db
}

func sqliteSetup(b *testing.B, db *sql.DB) {
	b.Helper()
	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT, age INTEGER, score REAL)"); err != nil {
		b.Fatalf("CREATE: %v", err)
	}
}

func insertRows(b *testing.B, db *sql.DB, n int) {
	b.Helper()
	for i := 0; i < n; i++ {
		if _, err := db.Exec("INSERT INTO t VALUES (?, ?, ?, ?)", i, fmt.Sprintf("user%d", i), 20+i%50, float64(i)*1.5); err != nil {
			b.Fatalf("INSERT %d: %v", i, err)
		}
	}
}

// ── INSERT ──

func BenchmarkRazordata_Insert(b *testing.B) {
	db := razorOpen(b)
	defer db.Close()
	razorSetup(b, db)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		db.Exec("DELETE FROM t")
		b.StartTimer()
		insertRows(b, db, benchRows)
	}
}

func BenchmarkSQLite_Insert(b *testing.B) {
	db := sqliteOpen(b)
	defer db.Close()
	sqliteSetup(b, db)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		db.Exec("DELETE FROM t")
		b.StartTimer()
		insertRows(b, db, benchRows)
	}
}

// ── SELECT * (full scan) ──

func BenchmarkRazordata_SelectAll(b *testing.B) {
	db := razorOpen(b)
	defer db.Close()
	razorSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Query("SELECT * FROM t")
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			var id int64
			var name string
			var age int64
			var score float64
			rows.Scan(&id, &name, &age, &score)
		}
		rows.Close()
	}
}

func BenchmarkSQLite_SelectAll(b *testing.B) {
	db := sqliteOpen(b)
	defer db.Close()
	sqliteSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Query("SELECT * FROM t")
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			var id int64
			var name string
			var age int64
			var score float64
			rows.Scan(&id, &name, &age, &score)
		}
		rows.Close()
	}
}

// ── SELECT with WHERE ──

func BenchmarkRazordata_SelectWhere(b *testing.B) {
	db := razorOpen(b)
	defer db.Close()
	razorSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Query("SELECT id, name, score FROM t WHERE age > 30 AND age < 50")
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			var id int64
			var name string
			var score float64
			rows.Scan(&id, &name, &score)
		}
		rows.Close()
	}
}

func BenchmarkSQLite_SelectWhere(b *testing.B) {
	db := sqliteOpen(b)
	defer db.Close()
	sqliteSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Query("SELECT id, name, score FROM t WHERE age > 30 AND age < 50")
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			var id int64
			var name string
			var score float64
			rows.Scan(&id, &name, &score)
		}
		rows.Close()
	}
}

// ── SELECT with ORDER BY + LIMIT ──

func BenchmarkRazordata_SelectOrderByLimit(b *testing.B) {
	db := razorOpen(b)
	defer db.Close()
	razorSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Query("SELECT id, name, score FROM t ORDER BY score DESC LIMIT 10")
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			var id int64
			var name string
			var score float64
			rows.Scan(&id, &name, &score)
		}
		rows.Close()
	}
}

func BenchmarkSQLite_SelectOrderByLimit(b *testing.B) {
	db := sqliteOpen(b)
	defer db.Close()
	sqliteSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Query("SELECT id, name, score FROM t ORDER BY score DESC LIMIT 10")
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			var id int64
			var name string
			var score float64
			rows.Scan(&id, &name, &score)
		}
		rows.Close()
	}
}

// ── SELECT GROUP BY + aggregate ──

func BenchmarkRazordata_SelectGroupBy(b *testing.B) {
	db := razorOpen(b)
	defer db.Close()
	razorSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Query("SELECT age, COUNT(*), AVG(score) FROM t GROUP BY age")
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			var age int64
			var cnt int64
			var avg float64
			rows.Scan(&age, &cnt, &avg)
		}
		rows.Close()
	}
}

func BenchmarkSQLite_SelectGroupBy(b *testing.B) {
	db := sqliteOpen(b)
	defer db.Close()
	sqliteSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Query("SELECT age, COUNT(*), AVG(score) FROM t GROUP BY age")
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			var age int64
			var cnt int64
			var avg float64
			rows.Scan(&age, &cnt, &avg)
		}
		rows.Close()
	}
}

// ── SELECT COUNT(*) ──

func BenchmarkRazordata_SelectCount(b *testing.B) {
	db := razorOpen(b)
	defer db.Close()
	razorSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var n int64
		if err := db.QueryRow("SELECT COUNT(*) FROM t").Scan(&n); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSQLite_SelectCount(b *testing.B) {
	db := sqliteOpen(b)
	defer db.Close()
	sqliteSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var n int64
		if err := db.QueryRow("SELECT COUNT(*) FROM t").Scan(&n); err != nil {
			b.Fatal(err)
		}
	}
}

// ── UPDATE ──

func BenchmarkRazordata_Update(b *testing.B) {
	db := razorOpen(b)
	defer db.Close()
	razorSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.Exec("UPDATE t SET score = score + 1.0 WHERE age > 25"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSQLite_Update(b *testing.B) {
	db := sqliteOpen(b)
	defer db.Close()
	sqliteSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.Exec("UPDATE t SET score = score + 1.0 WHERE age > 25"); err != nil {
			b.Fatal(err)
		}
	}
}

// ── DELETE ──

func BenchmarkRazordata_Delete(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		db := razorOpen(b)
		razorSetup(b, db)
		insertRows(b, db, benchRows)
		b.StartTimer()
		if _, err := db.Exec("DELETE FROM t WHERE age > 40"); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		db.Close()
	}
}

func BenchmarkSQLite_Delete(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		db := sqliteOpen(b)
		sqliteSetup(b, db)
		insertRows(b, db, benchRows)
		b.StartTimer()
		if _, err := db.Exec("DELETE FROM t WHERE age > 40"); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		db.Close()
	}
}

// ── SELECT with IN list ──

func BenchmarkRazordata_SelectInList(b *testing.B) {
	db := razorOpen(b)
	defer db.Close()
	razorSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Query("SELECT id, name FROM t WHERE age IN (20, 25, 30, 35, 40)")
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			var id int64
			var name string
			rows.Scan(&id, &name)
		}
		rows.Close()
	}
}

func BenchmarkSQLite_SelectInList(b *testing.B) {
	db := sqliteOpen(b)
	defer db.Close()
	sqliteSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Query("SELECT id, name FROM t WHERE age IN (20, 25, 30, 35, 40)")
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			var id int64
			var name string
			rows.Scan(&id, &name)
		}
		rows.Close()
	}
}

// ── Self-JOIN ──

func BenchmarkRazordata_SelfJoin(b *testing.B) {
	db := razorOpen(b)
	defer db.Close()
	razorSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Query("SELECT a.id, b.name FROM t a INNER JOIN t b ON a.age = b.age WHERE a.id < b.id LIMIT 20")
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			var id int64
			var name string
			rows.Scan(&id, &name)
		}
		rows.Close()
	}
}

func BenchmarkSQLite_SelfJoin(b *testing.B) {
	db := sqliteOpen(b)
	defer db.Close()
	sqliteSetup(b, db)
	insertRows(b, db, benchRows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Query("SELECT a.id, b.name FROM t a INNER JOIN t b ON a.age = b.age WHERE a.id < b.id LIMIT 20")
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			var id int64
			var name string
			rows.Scan(&id, &name)
		}
		rows.Close()
	}
}
