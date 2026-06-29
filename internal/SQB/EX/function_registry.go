// Package EX function registry.
//
// REQ000978: replaces the flat `switch name` in evalFunction with a
// map-based registry. Adding a new function is a one-line
// registration rather than editing a switch block. The registry is
// populated at package init and is read-only after that.
//
// All native implementations are `func([]PS.Expr, *Row, []any) (Value, error)`
// — they evaluate arguments and return a Value directly without going
// through the any-typed `valueFromAnyWrap` bridge (REQ000978c).
package EX

import (
	"fmt"
	"strings"
	"time"

	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// scalarFuncImpl is the signature for a registry-resident scalar
// function implementation.
type scalarFuncImpl func(args []PS.Expr, row *Row, params []any) (Value, error)

// scalarFuncRegistry maps a function name to its implementation.
// All entries are populated at init time. The map is read-only
// after init — callers must not mutate it.
var scalarFuncRegistry = map[string]scalarFuncImpl{}

// registerScalarFunc adds an entry to the registry. Called only from
// init; panics on duplicate registration so the build fails loudly
// if a function is added twice.
func registerScalarFunc(name string, impl scalarFuncImpl) {
	if _, exists := scalarFuncRegistry[name]; exists {
		panic("EX: duplicate scalar function registration: " + name)
	}
	scalarFuncRegistry[name] = impl
}

// init populates the registry with all built-in scalar functions.
// This is the single point of registration — adding a new function
// requires only adding a `registerScalarFunc` line here and writing
// the function body in this file (or another file in the package).
func init() {
	// Native implementations live in this file.
	registerScalarFunc("LENGTH", evalLength)
	registerScalarFunc("UPPER", evalUpper)
	registerScalarFunc("LOWER", evalLower)
	registerScalarFunc("IFNULL", evalIfNull)
	registerScalarFunc("COALESCE", evalCoalesce)
	registerScalarFunc("NULLIF", evalNullIf)
	registerScalarFunc("NOW", evalNow)
	registerScalarFunc("SUBSTR", evalSubstrNative)
	registerScalarFunc("ABS", evalAbsNative)
	registerScalarFunc("HEX", evalHexNative)
	registerScalarFunc("ROUND", evalRoundNative)
	registerScalarFunc("CHAR", evalCharNative)
	registerScalarFunc("CONCAT", evalConcatNative)
	registerScalarFunc("CONCAT_WS", evalConcatWSNative)
	registerScalarFunc("FORMAT", evalFormatNative)
	registerScalarFunc("LTRIM", evalLtrimNative)
	registerScalarFunc("RTRIM", evalRtrimNative)
	registerScalarFunc("TRIM", evalTrimNative)
	registerScalarFunc("REPLACE", evalReplaceNative)
	registerScalarFunc("QUOTE", evalQuoteNative)
	registerScalarFunc("TYPEOF", evalTypeofNative)
	registerScalarFunc("OCTET_LENGTH", evalOctetLengthNative)
	registerScalarFunc("UNICODE", evalUnicodeNative)
	registerScalarFunc("SQLITE_VERSION", evalSqliteVersionNative)
	registerScalarFunc("SQLITE_SOURCE_ID", evalSqliteSourceIDNative)
	registerScalarFunc("IIF", evalIIFNative)
	registerScalarFunc("IF", evalIIFNative)
	registerScalarFunc("INSTR", evalInstrNative)
	registerScalarFunc("SIGN", evalSignNative)
	registerScalarFunc("MAX", evalMaxScalarNative)
	registerScalarFunc("MIN", evalMinScalarNative)
	registerScalarFunc("RANDOM", evalRandomNative)
	registerScalarFunc("RANDOMBLOB", evalRandomBlobNative)
	registerScalarFunc("ZEROBLOB", evalZeroblobNative)
	registerScalarFunc("GLOB", evalGlobNative)
	registerScalarFunc("LIKELIHOOD", evalLikelihoodNative)
	registerScalarFunc("LIKELY", evalLikelyNative)
	registerScalarFunc("SOUNDEX", evalSoundexNative)
	registerScalarFunc("UNHEX", evalUnhexNative)
	registerScalarFunc("UNISTR", evalUnistrNative)
	registerScalarFunc("UNLIKELY", evalUnlikelyNative)
	registerScalarFunc("CHANGES", evalChangesNative)
	registerScalarFunc("LAST_INSERT_ROWID", evalLastInsertRowIDNative)
	registerScalarFunc("TOTAL_CHANGES", evalTotalChangesNative)
}

// evalLength returns the length of a string argument.
func evalLength(args []PS.Expr, row *Row, params []any) (Value, error) {
	if len(args) != 1 {
		return NullValue(), fmt.Errorf("length: expected 1 arg, got %d", len(args))
	}
	v, err := EvalValue(args[0], row, params)
	if err != nil {
		return NullValue(), err
	}
	if v.Kind == KindText {
		return NewIntValue(int64(len(v.S))), nil
	}
	return NullValue(), nil
}

// evalUpper upper-cases a string argument.
func evalUpper(args []PS.Expr, row *Row, params []any) (Value, error) {
	if len(args) != 1 {
		return NullValue(), fmt.Errorf("upper: expected 1 arg, got %d", len(args))
	}
	v, err := EvalValue(args[0], row, params)
	if err != nil {
		return NullValue(), err
	}
	if v.Kind == KindText {
		return NewTextValue(strings.ToUpper(v.S)), nil
	}
	return v, nil
}

// evalLower lower-cases a string argument.
func evalLower(args []PS.Expr, row *Row, params []any) (Value, error) {
	if len(args) != 1 {
		return NullValue(), fmt.Errorf("lower: expected 1 arg, got %d", len(args))
	}
	v, err := EvalValue(args[0], row, params)
	if err != nil {
		return NullValue(), err
	}
	if v.Kind == KindText {
		return NewTextValue(strings.ToLower(v.S)), nil
	}
	return v, nil
}

// evalIfNull returns the first non-NULL argument.
func evalIfNull(args []PS.Expr, row *Row, params []any) (Value, error) {
	if len(args) != 2 {
		return NullValue(), fmt.Errorf("ifnull: expected 2 args, got %d", len(args))
	}
	v, err := EvalValue(args[0], row, params)
	if err != nil {
		return NullValue(), err
	}
	if v.Kind != KindNull {
		return v, nil
	}
	return EvalValue(args[1], row, params)
}

// evalCoalesce returns the first non-NULL argument.
func evalCoalesce(args []PS.Expr, row *Row, params []any) (Value, error) {
	for _, arg := range args {
		v, err := EvalValue(arg, row, params)
		if err != nil {
			return NullValue(), err
		}
		if v.Kind != KindNull {
			return v, nil
		}
	}
	return NullValue(), nil
}

// evalNullIf returns NULL if the two args are equal, else the first.
func evalNullIf(args []PS.Expr, row *Row, params []any) (Value, error) {
	if len(args) != 2 {
		return NullValue(), fmt.Errorf("nullif: expected 2 args, got %d", len(args))
	}
	a, err := EvalValue(args[0], row, params)
	if err != nil {
		return NullValue(), err
	}
	b, err := EvalValue(args[1], row, params)
	if err != nil {
		return NullValue(), err
	}
	if equalValueValue(a, b) {
		return NullValue(), nil
	}
	return a, nil
}

// evalNow returns the current UTC time as an RFC3339 string.
func evalNow(args []PS.Expr, row *Row, params []any) (Value, error) {
	return NewTextValue(time.Now().UTC().Format(time.RFC3339)), nil
}

// evalChangesNative returns the row count of the most recent INSERT/UPDATE/DELETE.
func evalChangesNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	ec := OP.ExecContextFromRow(row)
	if ec == nil {
		return NewIntValue(0), nil
	}
	return NewIntValue(ec.LastChanges), nil
}

// evalLastInsertRowIDNative returns the most recent successful INSERT rowid.
func evalLastInsertRowIDNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	acc := getSessionCounterAccessor()
	if acc == nil {
		return NewIntValue(0), nil
	}
	return NewIntValue(acc.LastInsertRowID(getCurrentSessionID())), nil
}

// evalTotalChangesNative returns the cumulative row count of all
// INSERT/UPDATE/DELETE statements since the connection opened.
func evalTotalChangesNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	ec := OP.ExecContextFromRow(row)
	if ec == nil {
		return NewIntValue(0), nil
	}
	return NewIntValue(ec.TotalChanges), nil
}

// evalSubstrNative returns the substring of a string.
func evalSubstrNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalSubstr(args, row, params))
}

// evalAbsNative returns the absolute value of a numeric expression.
func evalAbsNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalAbs(args, row, params))
}

// evalHexNative returns the hexadecimal encoding of a blob.
func evalHexNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalHex(args, row, params))
}

// evalRoundNative rounds a numeric value to a given precision.
func evalRoundNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalRound(args, row, params))
}

// evalCharNative returns the unicode character for the given codepoint.
func evalCharNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalChar(args, row, params))
}

// evalConcatNative concatenates all arguments as strings.
func evalConcatNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalConcat(args, row, params))
}

// evalConcatWSNative concatenates arguments with a separator.
func evalConcatWSNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalConcatWS(args, row, params))
}

// evalFormatNative formats a number with thousands separators and fixed decimals.
func evalFormatNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalFormat(args, row, params))
}

// evalLtrimNative removes leading whitespace (or a custom char set).
func evalLtrimNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalLtrim(args, row, params))
}

// evalRtrimNative removes trailing whitespace (or a custom char set).
func evalRtrimNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalRtrim(args, row, params))
}

// evalTrimNative removes leading and trailing whitespace (or a custom char set).
func evalTrimNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalTrim(args, row, params))
}

// evalReplaceNative replaces occurrences of a substring with a replacement.
func evalReplaceNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalReplace(args, row, params))
}

// evalQuoteNative SQL-quotes a value for use in a statement.
func evalQuoteNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalQuote(args, row, params))
}

// evalTypeofNative returns the type name of a value.
func evalTypeofNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalTypeof(args, row, params))
}

// evalOctetLengthNative returns the byte length of a string.
func evalOctetLengthNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalOctetLength(args, row, params))
}

// evalUnicodeNative returns the unicode codepoint of the first character.
func evalUnicodeNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalUnicode(args, row, params))
}

// evalSqliteVersionNative returns the SQLite version string.
func evalSqliteVersionNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalSqliteVersion(args, row, params))
}

// evalSqliteSourceIDNative returns the SQLite source ID string.
func evalSqliteSourceIDNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalSqliteSourceID(args, row, params))
}

// evalIIFNative returns arg2 if arg1 is true, else arg3.
func evalIIFNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalIIF(args, row, params))
}

// evalInstrNative returns the position of the first occurrence of arg2 in arg1.
func evalInstrNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalInstr(args, row, params))
}

// evalSignNative returns -1, 0, or 1 depending on the sign of the argument.
func evalSignNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalSign(args, row, params))
}

// evalMaxScalarNative returns the maximum of the given arguments (scalar variant).
func evalMaxScalarNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalMaxScalar(args, row, params))
}

// evalMinScalarNative returns the minimum of the given arguments (scalar variant).
func evalMinScalarNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalMinScalar(args, row, params))
}

// evalRandomNative returns a pseudo-random integer.
func evalRandomNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalRandom(args, row, params))
}

// evalRandomBlobNative returns a random BLOB of the given byte length.
func evalRandomBlobNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalRandomBlob(args, row, params))
}

// evalZeroblobNative returns a BLOB of the given byte length filled with zeros.
func evalZeroblobNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalZeroblob(args, row, params))
}

// evalGlobNative tests whether a string matches a GLOB pattern.
func evalGlobNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalGlob(args, row, params))
}

// evalLikelihoodNative is a no-op hint for the query planner.
func evalLikelihoodNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalLikelihood(args, row, params))
}

// evalLikelyNative is a no-op hint that the argument is likely true.
func evalLikelyNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalLikely(args, row, params))
}

// evalSoundexNative returns the Soundex encoding of a string.
func evalSoundexNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalSoundex(args, row, params))
}

// evalUnhexNative decodes a hex-encoded string to a BLOB.
func evalUnhexNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalUnhex(args, row, params))
}

// evalUnistrNative builds a string from unicode codepoint values.
func evalUnistrNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalUnistr(args, row, params))
}

// evalUnlikelyNative is a no-op hint that the argument is likely false.
func evalUnlikelyNative(args []PS.Expr, row *Row, params []any) (Value, error) {
	return valueFromAnyWrap(evalUnlikely(args, row, params))
}
