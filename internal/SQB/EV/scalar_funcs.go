package EV

import (
	"encoding/hex"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func EvalAbs(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	switch v.Kind {
	case KindInt:
		if v.I64 == math.MinInt64 {
			return nil, fmt.Errorf("abs: integer overflow")
		}
		if v.I64 < 0 {
			return -v.I64, nil
		}
		return v.I64, nil
	case KindFloat:
		if v.F64 < 0 {
			return -v.F64, nil
		}
		return v.F64, nil
	}
	return 0.0, nil
}

func evalHex(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	switch v.Kind {
	case KindInt:
		// SQLite converts the integer to its text form first,
		// then hex-encodes that text. HEX(255) → "323535"
		// (the hex of the three ASCII digits).
		// REQ000763: use strconv.AppendInt + hex.Encode to
		// avoid intermediate []byte allocation from FormatInt.
		buf := make([]byte, 0, 24)
		buf = strconv.AppendInt(buf, v.I64, 10)
		h := make([]byte, hex.EncodedLen(len(buf)))
		hex.Encode(h, buf)
		for i, c := range h {
			if c >= 'a' && c <= 'f' {
				h[i] = c - 32
			}
		}
		return string(h), nil
	case KindFloat:
		buf := make([]byte, 0, 32)
		buf = strconv.AppendFloat(buf, v.F64, 'g', -1, 64)
		h := make([]byte, hex.EncodedLen(len(buf)))
		hex.Encode(h, buf)
		for i, c := range h {
			if c >= 'a' && c <= 'f' {
				h[i] = c - 32
			}
		}
		return string(h), nil
	case KindBlob:
		// REQ001031: use hex.Encode + in-place uppercase to avoid
		// the intermediate string allocation from EncodeToString.
		h := make([]byte, hex.EncodedLen(len(v.B)))
		hex.Encode(h, v.B)
		for i, c := range h {
			if c >= 'a' && c <= 'f' {
				h[i] = c - 32
			}
		}
		return string(h), nil
	case KindText:
		// REQ001031: hex-encode the string bytes directly.
		h := make([]byte, hex.EncodedLen(len(v.S)))
		hex.Encode(h, []byte(v.S))
		for i, c := range h {
			if c >= 'a' && c <= 'f' {
				h[i] = c - 32
			}
		}
		return string(h), nil
	default:
		s := DT.ValueToString(v)
		h := make([]byte, hex.EncodedLen(len(s)))
		hex.Encode(h, []byte(s))
		for i, c := range h {
			if c >= 'a' && c <= 'f' {
				h[i] = c - 32
			}
		}
		return string(h), nil
	}
}

func evalRound(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	// REQ000772: int64 fast path when no places arg is given or
	// places==0. Avoid the float64 conversion in numericFloat for
	// the common case of ROUND(int_col).
	if len(args) == 1 {
		if v.Kind == KindInt {
			return v.I64, nil
		}
	}
	var x float64
	var ok bool
	if v.Kind == KindInt {
		x = float64(v.I64)
		ok = true
	} else if v.Kind == KindFloat {
		x = v.F64
		ok = true
	}
	if !ok {
		return 0.0, nil
	}
	places := int64(0)
	if len(args) == 2 {
		pv, err := evalFallbackEvalValue(args[1], row, params)
		if err != nil {
			return nil, err
		}
		if pv.Kind != KindNull {
			if pv.Kind == KindInt {
				if pv.I64 < 0 {
					places = 0
				} else {
					places = pv.I64
				}
			} else if pv.Kind == KindFloat {
				p := int64(pv.F64)
				if p < 0 {
					places = 0
				} else {
					places = p
				}
			}
		}
	}
	mult := math.Pow(10, float64(places))
	return math.Round(x*mult) / mult, nil
}

// evalSubstr implements SUBSTR(str, start[, length]).
//   - start is 1-based per SQL convention; values <= 0 are clamped to 1.
//   - length is optional; when omitted the substring runs to the end of str.
//   - non-string inputs are coerced via fmt.Sprint.
func evalSubstr(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 2 {
		return nil, ErrEval
	}
	rawStr, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	// REQ000606: SUBSTR(NULL, ...) and SUBSTR(s, NULL, ...) must
	// return NULL, not "<nil>" (the fmt.Sprint result).
	if rawStr.Kind == KindNull {
		return nil, nil
	}
	// REQ001040: fast path for text values — avoid boxing + fmt.Sprint.
	var s string
	switch rawStr.Kind {
	case KindText:
		s = rawStr.S
	case KindBlob:
		s = string(rawStr.B)
	default:
		s = rawStr.String()
	}
	startV, err := evalFallbackEvalValue(args[1], row, params)
	if err != nil {
		return nil, err
	}
	if startV.Kind == KindNull {
		return nil, nil
	}
	var start int64
	var startOk bool
	if startV.Kind == KindInt {
		start = startV.I64
		startOk = true
	} else if startV.Kind == KindFloat {
		start = int64(startV.F64)
		startOk = true
	}
	if !startOk {
		return nil, ErrEval
	}
	if start < 0 {
		start = int64(len(s)) + start + 1
		if start < 1 {
			start = 1
		}
	} else if start == 0 {
		start = 1
	}
	// Convert 1-based start to 0-based offset.
	offset := int(start) - 1
	if offset >= len(s) {
		return "", nil
	}
	if len(args) >= 3 {
		lenV, err := evalFallbackEvalValue(args[2], row, params)
		if err != nil {
			return nil, err
		}
		var length int64
		var lenOk bool
		if lenV.Kind == KindInt {
			length = lenV.I64
			lenOk = true
		} else if lenV.Kind == KindFloat {
			length = int64(lenV.F64)
			lenOk = true
		}
		if !lenOk {
			return nil, ErrEval
		}
		if length < 0 {
			return "", nil
		}
		end := offset + int(length)
		if end > len(s) {
			end = len(s)
		}
		return s[offset:end], nil
	}
	return s[offset:], nil
}

// evalChar converts integer Unicode code points to a UTF-8 string.
// REQ000386.
func evalChar(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) == 0 {
		return "", nil
	}
	var sb strings.Builder
	for _, arg := range args {
		v, err := evalFallbackEvalValue(arg, row, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			return nil, nil // Any NULL arg → NULL result
		}
		var n int64
		if v.Kind == KindInt {
			n = v.I64
		} else if v.Kind == KindFloat {
			n = int64(v.F64)
		} else {
			continue
		}
		if n < 0 || n > unicode.MaxRune {
			continue
		}
		sb.WriteRune(rune(n))
	}
	return sb.String(), nil
}

// evalConcat concatenates all arguments into a single string.
// If any argument is NULL, the result is NULL. REQ000387.
func evalConcat(args []PS.Expr, row *Row, params []any) (any, error) {
	var sb strings.Builder
	for _, arg := range args {
		v, err := evalFallbackEvalValue(arg, row, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			return nil, nil // Any NULL → NULL result
		}
		sb.WriteString(DT.ValueToString(v))
	}
	return sb.String(), nil
}

// evalConcatWS concatenates with separator. First arg is separator.
// SEP=NULL → NULL. Skips NULL values. REQ000388.
func evalConcatWS(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 2 {
		return nil, ErrEval
	}
	sep, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if sep.Kind == KindNull {
		return nil, nil // NULL separator → NULL result
	}
	sepStr := DT.ValueToString(sep)
	var sb strings.Builder
	first := true
	for i := 1; i < len(args); i++ {
		v, err := evalFallbackEvalValue(args[i], row, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue // Skip NULL values
		}
		if !first {
			sb.WriteString(sepStr)
		}
		sb.WriteString(DT.ValueToString(v))
		first = false
	}
	return sb.String(), nil
}

// evalFormat implements printf-style formatting. REQ000389.
func evalFormat(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, ErrEval
	}
	fmtV, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if fmtV.Kind == KindNull {
		return nil, nil
	}
	fmtStr := fmtV.S
	if fmtV.Kind != KindText {
		fmtStr = fmtV.String()
	}
	// Convert remaining args to any for fmt.Sprintf
	fmtArgs := make([]any, len(args)-1)
	for i := 1; i < len(args); i++ {
		v, err := evalFallbackEvalValue(args[i], row, params)
		if err != nil {
			return nil, err
		}
		fmtArgs[i-1] = v.ToAny()
	}
	return fmt.Sprintf(fmtStr, fmtArgs...), nil
}

// evalLtrim trims leading characters. Default trim chars are spaces.
// REQ000397.
func evalLtrim(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	s := DT.ValueToString(v)
	if len(args) >= 2 {
		trimV, err := evalFallbackEvalValue(args[1], row, params)
		if err != nil {
			return nil, err
		}
		if trimV.Kind != KindNull {
			return strings.TrimLeft(s, DT.ValueToString(trimV)), nil
		}
	}
	return strings.TrimLeft(s, " "), nil
}

// evalRtrim trims trailing characters. Default trim chars are spaces.
// REQ000406.
func evalRtrim(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	s := DT.ValueToString(v)
	if len(args) >= 2 {
		trimV, err := evalFallbackEvalValue(args[1], row, params)
		if err != nil {
			return nil, err
		}
		if trimV.Kind != KindNull {
			return strings.TrimRight(s, DT.ValueToString(trimV)), nil
		}
	}
	return strings.TrimRight(s, " "), nil
}

// evalTrim trims leading and trailing characters. Default trim chars are spaces.
// REQ000438.
func evalTrim(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	s := DT.ValueToString(v)
	if len(args) >= 2 {
		trimV, err := evalFallbackEvalValue(args[1], row, params)
		if err != nil {
			return nil, err
		}
		if trimV.Kind != KindNull {
			return strings.Trim(s, DT.ValueToString(trimV)), nil
		}
	}
	return strings.Trim(s, " "), nil
}

// evalReplace replaces all occurrences of Y in X with Z.
// REQ000404.
func evalReplace(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 3 {
		return nil, ErrEval
	}
	x, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if x.Kind == KindNull {
		return nil, nil
	}
	y, err := evalFallbackEvalValue(args[1], row, params)
	if err != nil {
		return nil, err
	}
	z, err := evalFallbackEvalValue(args[2], row, params)
	if err != nil {
		return nil, err
	}
	xs := DT.ValueToString(x)
	if y.Kind == KindNull {
		return xs, nil // NULL pattern → return X unchanged
	}
	ys := DT.ValueToString(y)
	zs := ""
	if z.Kind != KindNull {
		zs = DT.ValueToString(z)
	}
	return strings.ReplaceAll(xs, ys, zs), nil
}

// evalQuote renders X as an SQL literal. Strings are single-quoted
// with escaped quotes. BLOBs as X'hex'. NULL as unquoted NULL.
// REQ000401.
func evalQuote(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return "NULL", nil
	}
	switch v.Kind {
	case KindText:
		// Escape single quotes by doubling
		escaped := strings.ReplaceAll(v.S, "'", "''")
		return "'" + escaped + "'", nil
	case KindInt, KindFloat, KindBool:
		// Numbers and booleans are not quoted
		return DT.ValueToString(v), nil
	default:
		return "'" + strings.ReplaceAll(DT.ValueToString(v), "'", "''") + "'", nil
	}
}

// evalTypeof returns the type name of X: "null", "integer", "real",
// "text", or "blob". REQ000412.
func evalTypeof(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return "null", nil
	}
	switch v.Kind {
	case KindInt, KindBool:
		return "integer", nil
	case KindFloat:
		return "real", nil
	case KindText:
		return "text", nil
	case KindBlob:
		return "blob", nil
	default:
		return "text", nil
	}
}

// evalOctetLength returns the byte length of X (not code points).
// REQ000400.
func evalOctetLength(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	// REQ000621: for []byte return the raw byte length, not the
	// fmt.Sprint representation (which yields "[104 101 ...]" for
	// "Hello"). For strings, return the byte length directly too
	// rather than going through fmt.Sprint.
	if v.Kind == KindBlob {
		return int64(len(v.B)), nil
	}
	if v.Kind == KindText {
		return int64(len(v.S)), nil
	}
	return int64(len(DT.ValueToString(v))), nil
}

// evalUnicode returns the Unicode code point of the first character.
// REQ000414.
func evalUnicode(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	s := DT.ValueToString(v)
	if len(s) == 0 {
		return int64(0), nil
	}
	r, _ := utf8.DecodeRuneInString(s)
	return int64(r), nil
}

// evalSqliteVersion returns the version string "0.26.7".
// REQ000410.
func evalSqliteVersion(args []PS.Expr, row *Row, params []any) (any, error) {
	return "0.26.7", nil
}

// evalSqliteSourceID returns "razordata-v0.26.7".
// REQ000409.
func evalSqliteSourceID(args []PS.Expr, row *Row, params []any) (any, error) {
	return "razordata-v0.26.7", nil
}

// evalIIF implements the iif(B, X, Y) conditional function.
// Short-circuits: only evaluates chosen branch. REQ000392.
func evalIIF(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 3 {
		return nil, ErrEval
	}
	cond, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if isValueTruthy(cond) {
		return evalFallbackEvalValue(args[1], row, params)
	}
	return evalFallbackEvalValue(args[2], row, params)
}

// evalInstr returns the 1-based position of Y in X, or 0 if not found.
// REQ000393.
func evalInstr(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 2 {
		return nil, ErrEval
	}
	x, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	// REQ000620: NULL on either side must return NULL, not 0.
	if x.Kind == KindNull {
		return nil, nil
	}
	y, err := evalFallbackEvalValue(args[1], row, params)
	if err != nil {
		return nil, err
	}
	if y.Kind == KindNull {
		return nil, nil
	}
	xs := DT.ValueToString(x)
	ys := DT.ValueToString(y)
	if ys == "" {
		return int64(1), nil
	}
	pos := strings.Index(xs, ys)
	if pos < 0 {
		return int64(0), nil
	}
	return int64(pos + 1), nil // 1-based
}

// evalSign returns -1, 0, or +1 based on the sign of X.
// REQ000407.
func evalSign(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	// REQ000619: SIGN(NULL) must return NULL, not 0.
	if v.Kind == KindNull {
		return nil, nil
	}
	// REQ000772: int64 fast path — SIGN on int64 only needs a
	// single comparison instead of the float64 conversion in
	// numericFloat.
	if v.Kind == KindInt {
		if v.I64 < 0 {
			return int64(-1), nil
		} else if v.I64 > 0 {
			return int64(1), nil
		}
		return int64(0), nil
	}
	if v.Kind == KindFloat {
		if v.F64 < 0 {
			return int64(-1), nil
		} else if v.F64 > 0 {
			return int64(1), nil
		}
		return int64(0), nil
	}
	return int64(0), nil
}

// evalMaxScalar returns the maximum of multiple scalar arguments.
// NULLs are skipped. REQ000398.
func evalMaxScalar(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) == 0 {
		return nil, nil
	}
	var maxV any
	for _, arg := range args {
		v, err := evalFallbackEvalValue(arg, row, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue
		}
		val := v.ToAny()
		if maxV == nil {
			maxV = val
			continue
		}
		// Compare with current max
		if compare(val, maxV) > 0 {
			maxV = val
		}
	}
	return maxV, nil
}

// evalMinScalar returns the minimum of multiple scalar arguments.
// REQ000399.
func evalMinScalar(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) == 0 {
		return nil, nil
	}
	var minV any
	for _, arg := range args {
		v, err := evalFallbackEvalValue(arg, row, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue
		}
		val := v.ToAny()
		if minV == nil {
			minV = val
			continue
		}
		if compare(val, minV) < 0 {
			minV = val
		}
	}
	return minV, nil
}

// evalRandom returns a pseudo-random int64. REQ000402.
func evalRandom(args []PS.Expr, row *Row, params []any) (any, error) {
	sign := 1
	if rand.IntN(2) == 1 {
		sign = -1
	}
	return int64(sign * int(rand.Int64())), nil
}

// evalRandomBlob returns N bytes of random data. REQ000403.
func evalRandomBlob(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	nV, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	var n int64
	if nV.Kind == KindInt {
		n = nV.I64
	} else if nV.Kind == KindFloat {
		n = int64(nV.F64)
	} else {
		return nil, nil
	}
	if n < 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte(rand.IntN(256))
	}
	return buf, nil
}

// evalZeroblob returns N bytes of 0x00. REQ000417.
func evalZeroblob(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	nV, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	var n int64
	if nV.Kind == KindInt {
		n = nV.I64
	} else if nV.Kind == KindFloat {
		n = int64(nV.F64)
	} else {
		return nil, nil
	}
	if n < 0 {
		return nil, nil
	}
	return make([]byte, n), nil
}

func evalGlob(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("glob requires 2 args")
	}
	pattern, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if pattern.Kind == KindNull {
		return nil, nil
	}
	str, err := evalFallbackEvalValue(args[1], row, params)
	if err != nil {
		return nil, err
	}
	if str.Kind == KindNull {
		return nil, nil
	}
	if pattern.Kind != KindText {
		return nil, nil
	}
	if str.Kind != KindText {
		return nil, nil
	}
	if globMatch(pattern.S, str.S) {
		return int64(1), nil
	}
	return int64(0), nil
}

// globMatch implements SQL GLOB pattern matching as a direct
// byte-matcher. REQ000581: the previous implementation called
// regexp.MustCompile on every invocation, allocating a new
// regex per row. For "WHERE name GLOB '*.txt'" over 10K rows
// that was 10K identical compiles. SQL GLOB supports only three
// wildcard forms (*, ?, [...]) which are easy to match without
// a regex engine.
func globMatch(pattern, s string) bool {
	return globMatchFrom(pattern, 0, s, 0)
}

// globMatchFrom is the recursive worker. Returns true if the
// remainder of pattern starting at pi matches the remainder of
// s starting at si.
func globMatchFrom(pattern string, pi int, s string, si int) bool {
	for pi < len(pattern) {
		c := pattern[pi]
		switch c {
		case '*':
			// * matches any number of characters. Try matching
			// the rest of the pattern against every suffix of s.
			for skip := si; skip <= len(s); skip++ {
				if globMatchFrom(pattern, pi+1, s, skip) {
					return true
				}
			}
			return false
		case '?':
			if si >= len(s) {
				return false
			}
			pi++
			si++
		case '[':
			// Character class: [abc], [a-z], [^abc]. We support
			// single chars and ranges; negation with leading ^.
			if si >= len(s) {
				return false
			}
			pi++
			negate := false
			if pi < len(pattern) && pattern[pi] == '^' {
				negate = true
				pi++
			}
			matched := false
			for pi < len(pattern) && pattern[pi] != ']' {
				lo := pattern[pi]
				pi++
				if pi+1 < len(pattern) && pattern[pi] == '-' && pattern[pi+1] != ']' {
					hi := pattern[pi+1]
					pi += 2
					if lo <= s[si] && s[si] <= hi {
						matched = true
					}
				} else {
					if lo == s[si] {
						matched = true
					}
				}
			}
			if pi < len(pattern) {
				pi++ // skip ']'
			}
			if matched == negate {
				return false
			}
			si++
		case '\\':
			if pi+1 >= len(pattern) {
				return false
			}
			pi++
			if si >= len(s) || pattern[pi] != s[si] {
				return false
			}
			pi++
			si++
		default:
			if si >= len(s) || c != s[si] {
				return false
			}
			pi++
			si++
		}
	}
	return si == len(s)
}

// evalLikelihood implements likelihood(X,Y) — no-op pass-through.
// REQ000395.
func evalLikelihood(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	return evalFallbackEvalValue(args[0], row, params)
}

// evalLikely implements likely(X) — no-op pass-through.
// REQ000396.
func evalLikely(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	return evalFallbackEvalValue(args[0], row, params)
}

// evalSoundex implements soundex(X) — 4-char phonetic encoding.
// REQ000408.
// evalSoundex implements soundex(X) — 4-char phonetic encoding.
// REQ000408.
// REQ000591: pre-allocated soundex encoding map. Kept at package
// level to avoid re-allocation on every evalSoundex call.
var soundexCodes = map[byte]byte{
	'B': '1', 'F': '1', 'P': '1', 'V': '1',
	'C': '2', 'G': '2', 'J': '2', 'K': '2', 'Q': '2', 'S': '2', 'X': '2', 'Z': '2',
	'D': '3', 'T': '3',
	'L': '4',
	'M': '5', 'N': '5',
	'R': '6',
}

func evalSoundex(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	val, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if val.Kind == KindNull {
		return nil, nil
	}
	if val.Kind != KindText {
		return "?000", nil
	}
	s := val.S
	if s == "" {
		return "?000", nil
	}

	// Convert to uppercase, keep only letters
	var letters []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			letters = append(letters, c-32) // to upper
		} else if c >= 'A' && c <= 'Z' {
			letters = append(letters, c)
		}
	}

	if len(letters) == 0 {
		return "?000", nil
	}

	codes := soundexCodes

	result := make([]byte, 0, 4)
	result = append(result, letters[0])

	prevCode := codes[letters[0]]

	for i := 1; i < len(letters) && len(result) < 4; i++ {
		code := codes[letters[i]]

		if code != 0 && code != prevCode {
			result = append(result, code)
		}

		// Vowels (and H, W) don't separate same-code consonants
		isVowel := letters[i] == 'A' || letters[i] == 'E' || letters[i] == 'I' ||
			letters[i] == 'O' || letters[i] == 'U' || letters[i] == 'Y' ||
			letters[i] == 'H' || letters[i] == 'W'

		if !isVowel {
			prevCode = code
		}
	}

	for len(result) < 4 {
		result = append(result, '0')
	}

	return string(result), nil
}

// REQ000413.
func evalUnhex(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	val, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if val.Kind == KindNull {
		return nil, nil
	}
	if val.Kind != KindText {
		return nil, nil
	}
	s := val.S

	// Decode hex string
	s = strings.TrimSpace(s)
	if len(s)%2 != 0 {
		return nil, nil
	}

	result := make([]byte, 0, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		h1 := hexDigit(s[i])
		h2 := hexDigit(s[i+1])
		if h1 == 0xff || h2 == 0xff {
			return nil, nil
		}
		result = append(result, h1<<4|h2)
	}
	return result, nil
}

func hexDigit(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	default:
		return 0xff
	}
}

// evalUnistr implements unistr(X) — backslash-escape decoder.
// REQ000415.
func evalUnistr(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	val, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if val.Kind == KindNull {
		return nil, nil
	}
	if val.Kind != KindText {
		return nil, nil
	}
	s := val.S

	var result strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			result.WriteByte(s[i])
			continue
		}

		i++
		switch s[i] {
		case 'u':
			if i+4 >= len(s) {
				result.WriteByte('\\')
				result.WriteByte('u')
				break
			}
			hexStr := s[i+1 : i+5]
			if r, ok := parseHex4(hexStr); ok {
				result.WriteRune(r)
				i += 4
			} else {
				result.WriteByte('\\')
				result.WriteByte('u')
				result.WriteString(hexStr)
				i += 4
			}
		case 'U':
			if i+8 >= len(s) {
				result.WriteByte('\\')
				result.WriteByte('U')
				break
			}
			hexStr := s[i+1 : i+9]
			if r, ok := parseHex8(hexStr); ok {
				result.WriteRune(r)
				i += 8
			} else {
				result.WriteByte('\\')
				result.WriteByte('U')
				result.WriteString(hexStr)
				i += 8
			}
		case '+':
			if i+6 >= len(s) {
				result.WriteByte('\\')
				result.WriteByte('+')
				break
			}
			hexStr := s[i+1 : i+7]
			if r, ok := parseHex6(hexStr); ok {
				result.WriteRune(r)
				i += 6
			} else {
				result.WriteByte('\\')
				result.WriteByte('+')
				result.WriteString(hexStr)
				i += 6
			}
		case 'n':
			result.WriteByte('\n')
		case 'r':
			result.WriteByte('\r')
		case 't':
			result.WriteByte('\t')
		case '\\':
			result.WriteByte('\\')
		default:
			result.WriteByte('\\')
			result.WriteByte(s[i])
		}
	}

	return result.String(), nil
}

func parseHex4(s string) (rune, bool) {
	v, err := strconv.ParseUint(s, 16, 16)
	if err != nil {
		return 0, false
	}
	return rune(v), true
}

func parseHex6(s string) (rune, bool) {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, false
	}
	return rune(v), true
}

func parseHex8(s string) (rune, bool) {
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, false
	}
	return rune(v), true
}

// evalUnlikely implements unlikely(X) — no-op pass-through.
// REQ000416.
func evalUnlikely(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	return evalFallbackEvalValue(args[0], row, params)
}

// Local comparison helpers.
