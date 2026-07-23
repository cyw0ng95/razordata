package PL

import (
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestREQ001972_EncodeMemoKey_MatchesNormalizePath proves the
// streaming encoder produces a digest byte-equivalent to the
// legacy NormalizeForMemo + SerializeKey path. This guards the
// memo cache from being invalidated by the rewrite. REQ001195.
func TestREQ001972_EncodeMemoKey_MatchesNormalizePath(t *testing.T) {
	stmt := &PS.Select{
		From: "t",
		Cols: []PS.Expr{&PS.StarExpr{}},
		Where: &PS.BinaryExpr{
			Op:    LX.T_EQ,
			Left:  &PS.Ident{Name: "a"},
			Right: &PS.NumberLiteral{Val: 7},
		},
	}

	cloned, params1 := NormalizeForMemo(stmt)
	want := SerializeKey(cloned)

	got, params2 := EncodeMemoKey(stmt)
	if got != want {
		t.Fatalf("EncodeMemoKey digest %q != Normalize+Serialize %q", got, want)
	}
	if len(params1) != len(params2) {
		t.Fatalf("params count mismatch: %d vs %d", len(params1), len(params2))
	}
	for i := range params1 {
		if params1[i] != params2[i] {
			t.Fatalf("params[%d] mismatch: %v vs %v", i, params1[i], params2[i])
		}
	}
}

// TestREQ001972_EncodeMemoKey_StableAcrossLiterals verifies the
// REQ001195 invariant: structurally identical statements differing
// only in literal value share the same memo key.
func TestREQ001972_EncodeMemoKey_StableAcrossLiterals(t *testing.T) {
	mk := func(lit int64) *PS.Select {
		return &PS.Select{
			From: "t",
			Cols: []PS.Expr{&PS.StarExpr{}},
			Where: &PS.BinaryExpr{
				Op:    LX.T_EQ,
				Left:  &PS.Ident{Name: "a"},
				Right: &PS.NumberLiteral{Val: lit},
			},
		}
	}
	k1, _ := EncodeMemoKey(mk(1))
	k2, _ := EncodeMemoKey(mk(999))
	if k1 != k2 {
		t.Fatalf("parameterized keys differ across literals: %q vs %q", k1, k2)
	}
}

// TestREQ001972_EncodeMemoKey_DifferentStructure verifies that
// statements differing in column reference produce distinct keys.
func TestREQ001972_EncodeMemoKey_DifferentStructure(t *testing.T) {
	mk := func(col string) *PS.Select {
		return &PS.Select{
			From: "t",
			Cols: []PS.Expr{&PS.StarExpr{}},
			Where: &PS.BinaryExpr{
				Op:    LX.T_EQ,
				Left:  &PS.Ident{Name: col},
				Right: &PS.NumberLiteral{Val: 1},
			},
		}
	}
	ka, _ := EncodeMemoKey(mk("a"))
	kb, _ := EncodeMemoKey(mk("b"))
	if ka == kb {
		t.Fatalf("different cols should yield different keys, both = %q", ka)
	}
}

// TestREQ001972_EncodeMemoKey_ConcurrentSafe hammers EncodeMemoKey
// from many goroutines using goroutine-local statements. The
// streaming encoder must not race because it never shares AST
// memory across calls — the only shared state is the enc.buf slice,
// which is stack-allocated per call.
func TestREQ001972_EncodeMemoKey_ConcurrentSafe(t *testing.T) {
	makeStmt := func(lit int64) *PS.Select {
		return &PS.Select{
			From: "t",
			Cols: []PS.Expr{&PS.StarExpr{}},
			Where: &PS.BinaryExpr{
				Op:    LX.T_EQ,
				Left:  &PS.Ident{Name: "a"},
				Right: &PS.NumberLiteral{Val: lit},
			},
		}
	}

	const goroutines = 32
	const iters = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				key, _ := EncodeMemoKey(makeStmt(int64(seed*iters + i)))
				if key == "" {
					t.Errorf("empty key from goroutine %d iter %d", seed, i)
				}
			}
		}(g)
	}
	wg.Wait()
}