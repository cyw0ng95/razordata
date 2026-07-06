package sc

// TokenType represents a SQL data type. Defined here (before SQF in
// dependency order) so the storage catalog can use it without importing
// SQF/LX. Values match the corresponding LX.TokenType constants for
// data-type tokens so the on-disk format is preserved.
type TokenType int

// Standard SQL data type constants matching LX.TokenType values.
// These values are derived from counting the iota positions in LX/token.go.
const (
	T_Null TokenType = iota
	// Data-type tokens — values must match LX.TokenType
	T_Int       TokenType = 58 // LX.T_INT_KW
	T_BigInt    TokenType = 59 // LX.T_BIGINT
	T_Float     TokenType = 60 // LX.T_FLOAT_KW
	T_Bool      TokenType = 61 // LX.T_BOOL
	T_Text      TokenType = 62 // LX.T_TEXT
	T_Blob      TokenType = 63 // LX.T_BLOB
	T_Varchar   TokenType = 64 // LX.T_VARCHAR
	T_Timestamp TokenType = 65 // LX.T_TIMESTAMP
	T_Numeric   TokenType = 66 // LX.T_NUMERIC
	T_Date      TokenType = 67 // LX.T_DATE
	T_Time      TokenType = 68 // LX.T_TIME
	T_Json      TokenType = 69 // LX.T_JSON
	T_Decimal   TokenType = 70 // LX.T_DECIMAL
)
