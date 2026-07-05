package EX

import (
	"fmt"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// create8TablePlanner registers 8 tables like select4.
func create8TablePlanner() *Planner {
	p := NewPlanner()
	for _, ti := range []int{1, 2, 3, 4, 5, 6, 7, 8} {
		s := fmt.Sprintf("%d", ti)
		p.RegisterTable("t"+s, []DT.ColInfo{
			{Name: "a" + s, Typ: 1}, {Name: "b" + s, Typ: 1},
			{Name: "c" + s, Typ: 1}, {Name: "d" + s, Typ: 1},
			{Name: "e" + s, Typ: 1}, {Name: "x" + s, Typ: 5},
		}, "a"+s)
	}
	return p
}

func BenchmarkPlannerMissHit(b *testing.B) {
	// 8-table join (select4-like)
	b.Run("miss-8table-cold", func(b *testing.B) {
		// Pre-create b.N unique planners (cold cache) and stmts
		planners := make([]*Planner, b.N)
		stmts := make([]PS.Stmt, b.N)
		for i := 0; i < b.N; i++ {
			planners[i] = create8TablePlanner()
			stmts[i], _ = PS.NewParser(
				fmt.Sprintf("SELECT * FROM t1,t2,t3,t4,t5,t6,t7,t8 WHERE a1 = %d", i)).Parse()
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			result, err := planners[i].Plan(stmts[i])
			if err != nil {
				b.Fatal(err)
			}
			if result == nil {
				b.Fatal("nil plan")
			}
		}
	})

	b.Run("hit-8table", func(b *testing.B) {
		p := create8TablePlanner()
		// Prime cache
		prime, _ := PS.NewParser(
			"SELECT * FROM t1,t2,t3,t4,t5,t6,t7,t8 WHERE a1 = 0").Parse()
		_, err := p.Plan(prime)
		if err != nil {
			b.Fatal(err)
		}
		// Pre-create b.N stmts with same structure, different literals
		stmts := make([]PS.Stmt, b.N)
		for i := 0; i < b.N; i++ {
			stmts[i], _ = PS.NewParser(
				fmt.Sprintf("SELECT * FROM t1,t2,t3,t4,t5,t6,t7,t8 WHERE a1 = %d", (i%100)+1)).Parse()
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			result, err := p.Plan(stmts[i])
			if err != nil {
				b.Fatal(err)
			}
			if result == nil {
				b.Fatal("nil plan")
			}
		}
	})

	// Single-table for comparison
	b.Run("miss-simple-cold", func(b *testing.B) {
		planners := make([]*Planner, b.N)
		stmts := make([]PS.Stmt, b.N)
		for i := 0; i < b.N; i++ {
			p := NewPlanner()
			p.RegisterTable("t", []DT.ColInfo{{Name: "a", Typ: 1}}, "a")
			planners[i] = p
			stmts[i], _ = PS.NewParser(fmt.Sprintf("SELECT * FROM t WHERE a = %d", i)).Parse()
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			result, err := planners[i].Plan(stmts[i])
			if err != nil {
				b.Fatal(err)
			}
			if result == nil {
				b.Fatal("nil plan")
			}
		}
	})

	b.Run("hit-simple", func(b *testing.B) {
		p := NewPlanner()
		p.RegisterTable("t", []DT.ColInfo{{Name: "a", Typ: 1}}, "a")
		prime, _ := PS.NewParser("SELECT * FROM t WHERE a = 0").Parse()
		_, err := p.Plan(prime)
		if err != nil {
			b.Fatal(err)
		}
		stmts := make([]PS.Stmt, b.N)
		for i := 0; i < b.N; i++ {
			stmts[i], _ = PS.NewParser(
				fmt.Sprintf("SELECT * FROM t WHERE a = %d", (i%100)+1)).Parse()
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			result, err := p.Plan(stmts[i])
			if err != nil {
				b.Fatal(err)
			}
			if result == nil {
				b.Fatal("nil plan")
			}
		}
	})
}
