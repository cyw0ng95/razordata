package PS

import "github.com/cyw0ng95/razordata/internal/SQF/LX"

// TypeInfo carries type metadata for parameterized types
// (REQ000207).
type TypeInfo struct {
	Type      LX.TokenType // base type token
	Precision int          // for DECIMAL(P,S): P
	Scale     int          // for DECIMAL(P,S): S
	Size      int          // for VARCHAR(N), CHAR(N): N
}
