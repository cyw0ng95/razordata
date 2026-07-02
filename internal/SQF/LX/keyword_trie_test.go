package LX

import "testing"

// TestKeywordTrie_AllKeywordsFound verifies every keyword in
// the source list is reachable via lookupKeyword. This is a
// sanity check against typos in the trie construction.
func TestKeywordTrie_AllKeywordsFound(t *testing.T) {
	// Source list mirrors buildKeywordTrie. Must stay in
	// sync — if this test fails, somebody added a keyword to
	// one place but not the other.
	expected := map[string]TokenType{
		"CREATE": T_CREATE, "DROP": T_DROP, "INSERT": T_INSERT,
		"INTO": T_INTO, "UPDATE": T_UPDATE, "DELETE": T_DELETE,
		"SELECT": T_SELECT, "FROM": T_FROM, "WHERE": T_WHERE,
		"AND": T_AND, "OR": T_OR, "NOT": T_NOT, "IN": T_IN,
		"BETWEEN": T_BETWEEN, "LIKE": T_LIKE, "GLOB": T_GLOB,
		"IS": T_IS, "NULL": T_NULL,
		"BEGIN": T_BEGIN, "COMMIT": T_COMMIT, "ROLLBACK": T_ROLLBACK,
		"AS": T_AS, "BY": T_BY, "ASC": T_ASC, "DESC": T_DESC,
		"LIMIT": T_LIMIT, "OFFSET": T_OFFSET,
		"TABLE": T_TABLE, "INDEX": T_INDEX, "PRIMARY": T_PRIMARY,
		"KEY": T_KEY, "NOTNULL": T_NOTNULL, "DEFAULT": T_DEFAULT,
		"CHECK": T_CHECK, "UNIQUE": T_UNIQUE,
		"INTEGER": T_INT_KW, "INT": T_INT_KW, "BIGINT": T_BIGINT,
		"FLOAT": T_FLOAT_KW, "REAL": T_FLOAT_KW, "DOUBLE": T_FLOAT_KW,
		"BOOL": T_BOOL, "BOOLEAN": T_BOOL, "TEXT": T_TEXT,
		"BLOB": T_BLOB, "VARCHAR": T_VARCHAR, "TIMESTAMP": T_TIMESTAMP,
		"NUMERIC": T_NUMERIC, "DATE": T_DATE, "TIME": T_TIME,
		"JSON": T_JSON, "DECIMAL": T_DECIMAL,
		"VALUES": T_VALUES, "SET": T_SET, "ORDER": T_ORDER,
		"JOIN": T_JOIN, "LEFT": T_LEFT, "RIGHT": T_RIGHT,
		"INNER": T_INNER, "CROSS": T_CROSS,
		"ON": T_ON, "USING": T_USING, "GROUP": T_GROUP,
		"HAVING": T_HAVING, "COUNT": T_COUNT, "SUM": T_SUM,
		"AVG": T_AVG, "MIN": T_MIN, "MAX": T_MAX,
		"DISTINCT": T_DISTINCT, "CASE": T_CASE, "WHEN": T_WHEN,
		"THEN": T_THEN, "ELSE": T_ELSE, "END": T_END,
		"EXISTS": T_EXISTS, "ANALYZE": T_ANALYZE, "VACUUM": T_VACUUM,
		"TRUNCATE": T_TRUNCATE, "REINDEX": T_REINDEX,
		"BEFORE": T_BEFORE, "AFTER": T_AFTER, "INSTEAD": T_INSTEAD,
		"OF": T_OF, "FOR": T_FOR, "EACH": T_EACH,
		"OUTER": T_OUTER, "FULL": T_FULL, "INTERVAL": T_INTERVAL,
		"OVER": T_OVER, "PARTITION": T_PARTITION,
		"ROWS": T_ROWS, "RANGE": T_RANGE,
		"PRECEDING": T_PRECEDING, "FOLLOWING": T_FOLLOWING,
		"CURRENT": T_CURRENT, "UNBOUNDED": T_UNBOUNDED,
		"ROW_NUMBER": T_ROW_NUMBER, "RANK": T_RANK,
		"DENSE_RANK": T_DENSE_RANK,
		"LAG":        T_LAG, "LEAD": T_LEAD,
		"FIRST_VALUE": T_FIRST_VALUE, "LAST_VALUE": T_LAST_VALUE,
		"NTH_VALUE": T_NTH_VALUE, "ROW": T_ROW,
		"TRANSACTION": T_TRANSACTION, "ISOLATION": T_ISOLATION,
		"LEVEL": T_LEVEL, "COALESCE": T_COALESCE, "NULLIF": T_NULLIF,
		"UNION": T_UNION, "INTERSECT": T_INTERSECT, "EXCEPT": T_EXCEPT,
		"ALL": T_ALL, "AUTOINCREMENT": T_AUTOINCREMENT,
		"READ": T_READ, "COMMITTED": T_COMMITTED,
		"UNCOMMITTED": T_UNCOMMITTED, "REPEATABLE": T_REPEATABLE,
		"SERIALIZABLE": T_SERIALIZABLE, "VIEW": T_VIEW,
		"TRIGGER": T_TRIGGER, "TEMP": T_TEMP, "TEMPORARY": T_TEMPORARY,
		"ALTER": T_ALTER, "COLUMN": T_COLUMN, "ADD": T_ADD,
		"RENAME": T_RENAME, "FETCH": T_FETCH, "FIRST": T_FIRST,
		"NEXT": T_NEXT, "LAST": T_LAST, "ONLY": T_ONLY,
		"REFERENCES": T_REFERENCES, "FOREIGN": T_FOREIGN,
		"CASCADE": T_CASCADE, "RESTRICT": T_RESTRICT,
		"NO": T_NO, "ACTION": T_ACTION, "PRAGMA": T_PRAGMA,
		"CAST": T_CAST, "TRUE": T_TRUE, "FALSE": T_FALSE,
		"EXPLAIN": T_EXPLAIN, "QUERY": T_QUERY, "PLAN": T_PLAN,
		"RETURNING": T_RETURNING, "CONFLICT": T_CONFLICT,
		"DO": T_DO, "NOTHING": T_NOTHING, "EXCLUDED": T_EXCLUDED,
		"WITH": T_WITH, "SAVEPOINT": T_SAVEPOINT,
		"RELEASE": T_RELEASE, "TO": T_TO,
		"DEFERRED": T_DEFERRED, "IMMEDIATE": T_IMMEDIATE,
		"EXCLUSIVE": T_EXCLUSIVE, "RAISE": T_RAISE,
		"ESCAPE": T_ESCAPE, "INDEXED": T_INDEXED,
		"MATCH": T_MATCH, "DEFERRABLE": T_DEFERRABLE,
		"INITIALLY": T_INITIALLY, "COLLATE": T_COLLATE,
		"ATTACH": T_ATTACH, "DETACH": T_DETACH,
		"MATERIALIZED": T_MATERIALIZED, "REFRESH": T_REFRESH,
		"FILTER": T_FILTER, "EXCLUDE": T_EXCLUDE,
		"OTHERS": T_OTHERS, "TIES": T_TIES, "GROUPS": T_GROUPS,
	}
	for kw, want := range expected {
		gotLen, gotType := lookupKeyword(kw)
		if gotLen != len(kw) || gotType != want {
			t.Errorf("lookupKeyword(%q): got (%d, %v), want (%d, %v)", kw, gotLen, gotType, len(kw), want)
		}
	}
}

// TestKeywordTrie_CaseInsensitive verifies that the trie
// matches keywords regardless of letter case.
func TestKeywordTrie_CaseInsensitive(t *testing.T) {
	cases := []struct {
		input string
		want  TokenType
	}{
		{"select", T_SELECT},
		{"Select", T_SELECT},
		{"SELECT", T_SELECT},
		{"sElEcT", T_SELECT},
		{"from", T_FROM},
		{"integer", T_INT_KW},
		{"INTEGER", T_INT_KW},
	}
	for _, c := range cases {
		gotLen, gotType := lookupKeyword(c.input)
		if gotLen != len(c.input) || gotType != c.want {
			t.Errorf("lookupKeyword(%q): got (%d, %v), want (%d, %v)",
				c.input, gotLen, gotType, len(c.input), c.want)
		}
	}
}

// TestKeywordTrie_PrefixResolution verifies the longest-match
// rule: when a keyword is a prefix of another, the trie must
// return the longer one when the input matches all of it.
//
// lookupKeyword returns the LONGEST terminal reached while
// walking the trie. The caller (scanIdent) compares the
// returned length against the input length to decide
// keyword-vs-ident.
func TestKeywordTrie_PrefixResolution(t *testing.T) {
	cases := []struct {
		input    string
		wantLen  int
		wantType TokenType
	}{
		// INT and INTEGER both map to T_INT_KW. The longer
		// keyword (INTEGER) wins when input matches all of it.
		{"INT", 3, T_INT_KW},
		{"INTEGER", 7, T_INT_KW},
		// ROW vs ROW_NUMBER: distinct tokens; longer wins.
		{"ROW", 3, T_ROW},
		{"ROW_NUMBER", 10, T_ROW_NUMBER},
		// IN is itself a keyword; INDEX is also a keyword.
		// lookupKeyword("INDEX") walks past IN and finds the
		// deeper INDEX terminal.
		{"IN", 2, T_IN},
		{"INDEX", 5, T_INDEX},
		// NOT vs NOTHING: deeper terminal wins.
		{"NOT", 3, T_NOT},
		{"NOTHING", 7, T_NOTHING},
		// SELECTA: matches SELECT prefix (6 chars) but the
		// next char 'A' has no child, so the trie stops
		// there. Returns the SELECT terminal at depth 6.
		// Caller checks kwLen == len(ident) → 6 != 7 → ident.
		{"SELECTA", 6, T_SELECT},
	}
	for _, c := range cases {
		gotLen, gotType := lookupKeyword(c.input)
		if gotLen != c.wantLen || gotType != c.wantType {
			t.Errorf("lookupKeyword(%q): got (%d, %v), want (%d, %v)",
				c.input, gotLen, gotType, c.wantLen, c.wantType)
		}
	}
}

// TestKeywordTrie_NonKeywordReturnsZero verifies that strings
// sharing no prefix with any keyword return (0, T_EOF). The
// lexer uses this signal to treat the input as a plain
// identifier.
//
// NOTE: "selectx", "FROMX", "notinlist" all match keyword
// prefixes (SELECT, FROM, NOT) but the deeper walk fails, so
// lookupKeyword returns the SHORTER prefix match. The lexer
// then compares kwLen == len(ident) and rejects.
func TestKeywordTrie_NonKeywordReturnsZero(t *testing.T) {
	cases := []string{
		"",
		"myTable",
		"col_name",
		"x",
		"_underscore",
		"foo",
		"bar",
		"foobarbaz",
	}
	for _, c := range cases {
		gotLen, gotType := lookupKeyword(c)
		if gotLen != 0 || gotType != T_EOF {
			t.Errorf("lookupKeyword(%q): got (%d, %v), want (0, T_EOF)", c, gotLen, gotType)
		}
	}
}

// TestKeywordTrie_PrefixOnlyReturnsPrefix verifies that inputs
// which share a prefix with a keyword but don't extend it get
// the prefix keyword back. The lexer rejects these because
// kwLen != len(ident).
func TestKeywordTrie_PrefixOnlyReturnsPrefix(t *testing.T) {
	cases := []struct {
		input    string
		wantLen  int
		wantType TokenType
	}{
		{"SELECTX", 6, T_SELECT},
		{"FROMX", 4, T_FROM},
		{"notinlist", 3, T_NOT},
		{"INSIDE", 2, T_IN},
		// 'X' is a single char that is not in the trie.
		// The walk stops at depth 0 (no terminal reached).
	}
	for _, c := range cases {
		gotLen, gotType := lookupKeyword(c.input)
		if gotLen != c.wantLen || gotType != c.wantType {
			t.Errorf("lookupKeyword(%q): got (%d, %v), want (%d, %v)",
				c.input, gotLen, gotType, c.wantLen, c.wantType)
		}
	}
}

// TestKeywordTrie_EndToEndLexer verifies the trie integrates
// correctly with scanIdent: a token sequence produced by
// Next() must classify keywords and idents correctly across
// case mixes and prefix collisions.
func TestKeywordTrie_EndToEndLexer(t *testing.T) {
	cases := []struct {
		input string
		want  TokenType
	}{
		{"SELECT", T_SELECT},
		{"select", T_SELECT},
		{"Select", T_SELECT},
		{"myTable", T_IDENT},
		{"INT", T_INT_KW},
		{"INTEGER", T_INT_KW},
		{"ROW_NUMBER", T_ROW_NUMBER},
		{"ROW", T_ROW},
		{"INDEXED", T_INDEXED},
		// Prefix-of-keyword is NOT a keyword.
		{"SELECTA", T_IDENT},
		{"FROMX", T_IDENT},
		// Underscore-prefixed identifier.
		{"_underscore", T_IDENT},
	}
	for _, c := range cases {
		l := NewLexer(c.input)
		tok := l.Next()
		if tok.Type != c.want {
			t.Errorf("input %q: got %v, want %v", c.input, tok.Type, c.want)
		}
	}
}
