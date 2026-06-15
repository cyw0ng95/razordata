package EX

import (
	"github.com/cyw0ng95/razordata/internal/SQL/PL"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// serializeKey is the EX-side alias for PL.SerializeKey. The canonical
// implementation lives in PL/memo.go and is shared by every consumer.
func serializeKey(stmt PS.Stmt) string {
	return PL.SerializeKey(stmt)
}
