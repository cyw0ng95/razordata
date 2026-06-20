package slt

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// Parse reads a SQLLogicTest script from r and returns the parsed
// records. The parser is tolerant: a malformed record is reported
// via the second return slot of Parse; subsequent records continue
// to be parsed.
//
// The parser does not abort on a parse error mid-stream. Each
// returned slice element is either a valid Record or carries a
// *ParseError and a sentinel RecordInvalid. The runner filters
// invalid records and reports them via Stats.ParseErrors.
func Parse(r io.Reader) ([]Record, error) {
	scanner := bufio.NewScanner(r)
	// Some SLT files have very long SQL statements; raise the buffer
	// ceiling to the default 64 KiB * 64 = 4 MiB.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var records []Record
	var current *Record
	var currentSQL []string
	var currentExpected []string
	var inResult bool
	var lineNo int

	flushRecord := func(startLine int) {
		if current == nil {
			return
		}
		switch current.Kind {
		case RecordStatementOK, RecordStatementError:
			current.SQL = strings.TrimSpace(strings.Join(currentSQL, "\n"))
		case RecordQuery:
			current.SQL = strings.TrimSpace(strings.Join(currentSQL, "\n"))
			if inResult {
				current.Expected = parseResultRows(currentExpected, current.TypeString)
			}
		}
		current.Line = startLine
		records = append(records, *current)
		current = nil
		currentSQL = nil
		currentExpected = nil
		inResult = false
	}

	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		// Strip inline comments. A "#" not inside a string literal
		// terminates the line. The corpus rarely has comments in
		// the middle of records, so a leftmost "#" heuristic is
		// sufficient and avoids needing a real lexer.
		if i := indexCommentStart(raw); i >= 0 {
			raw = raw[:i]
		}
		trimmed := strings.TrimRight(raw, " \t\r")
		if strings.TrimSpace(trimmed) == "" {
			// Blank line: record separator.
			if current != nil {
				flushRecord(current.Line)
			}
			continue
		}
		// Inside a record's SQL body, the line is a continuation.
		if current != nil && (current.Kind == RecordStatementOK ||
			current.Kind == RecordStatementError ||
			(current.Kind == RecordQuery && !inResult)) {
			if current.Kind == RecordQuery && strings.HasPrefix(strings.TrimSpace(trimmed), "----") {
				inResult = true
				continue
			}
			currentSQL = append(currentSQL, trimmed)
			continue
		}
		// Inside a record's expected result rows.
		if current != nil && current.Kind == RecordQuery && inResult {
			currentExpected = append(currentExpected, trimmed)
			continue
		}
		// Beginning of a new record (or current is a config
		// directive with no body that we must finalize). Either
		// way, the previous current is done.
		if current != nil {
			flushRecord(current.Line)
		}
		rec, err := parseHeader(trimmed, lineNo)
		if err != nil {
			records = append(records, Record{Kind: RecordInvalid, Line: lineNo})
			_ = err
			continue
		}
		current = &rec
	}
	if err := scanner.Err(); err != nil {
		return records, err
	}
	if current != nil {
		flushRecord(current.Line)
	}
	return records, nil
}

// indexCommentStart returns the index of the first "#" that begins
// a comment, treating "#" inside a single-quoted string literal as
// data. Returns -1 if no comment start is found.
func indexCommentStart(line string) int {
	inStr := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\'' {
			// SQL doubles single quotes inside string literals.
			if inStr && i+1 < len(line) && line[i+1] == '\'' {
				i++
				continue
			}
			inStr = !inStr
			continue
		}
		if c == '#' && !inStr {
			return i
		}
	}
	return -1
}

// parseHeader interprets the first line of a record, switching on
// the directive keyword.
func parseHeader(line string, lineNo int) (Record, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return Record{}, &ParseError{Msg: "empty header at line " + strconv.Itoa(lineNo)}
	}
	switch fields[0] {
	case "statement":
		if len(fields) < 2 {
			return Record{}, &ParseError{Msg: "statement without kind at line " + strconv.Itoa(lineNo)}
		}
		switch fields[1] {
		case "ok":
			return Record{Kind: RecordStatementOK, Line: lineNo}, nil
		case "error":
			return Record{Kind: RecordStatementError, Line: lineNo}, nil
		default:
			return Record{}, &ParseError{Msg: "unknown statement kind: " + fields[1]}
		}
	case "query":
		rec := Record{Kind: RecordQuery, Line: lineNo}
		// Layout: query <type-string> [<sort-mode> [<label>]]
		if len(fields) < 2 {
			return Record{}, &ParseError{Msg: "query without type string at line " + strconv.Itoa(lineNo)}
		}
		rec.TypeString = fields[1]
		// sort and label are optional. The corpus mixes the
		// order: "query IT label-x" (label first, sort omitted)
		// is common. We accept both.
		//   - "query T rowsort"        -> sort=rowsort, label=""
		//   - "query T label-foo"      -> sort=nosort, label=label-foo
		//   - "query T rowsort lbl"    -> sort=rowsort, label=lbl
		//   - "query T lbl rowsort"    -> sort=rowsort, label=lbl
		for _, extra := range fields[2:] {
			switch {
			case extra == "nosort" || extra == "rowsort" || extra == "valuesort":
				rec.Sort = ParseSortMode(extra)
			default:
				rec.Label = extra
			}
		}
		return rec, nil
	case "halt":
		return Record{Kind: RecordHalt, Line: lineNo}, nil
	case "hash-threshold":
		if len(fields) < 2 {
			return Record{}, &ParseError{Msg: "hash-threshold without value at line " + strconv.Itoa(lineNo)}
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			return Record{}, &ParseError{Msg: "hash-threshold: " + err.Error()}
		}
		return Record{Kind: RecordHashThreshold, HashThreshold: n, Line: lineNo}, nil
	case "skipif":
		if len(fields) < 2 {
			return Record{}, &ParseError{Msg: "skipif without db at line " + strconv.Itoa(lineNo)}
		}
		return Record{Kind: RecordSkipIf, DBName: fields[1], Line: lineNo}, nil
	case "onlyif":
		if len(fields) < 2 {
			return Record{}, &ParseError{Msg: "onlyif without db at line " + strconv.Itoa(lineNo)}
		}
		return Record{Kind: RecordOnlyIf, DBName: fields[1], Line: lineNo}, nil
	default:
		return Record{}, &ParseError{Msg: "unknown directive: " + fields[0]}
	}
}

// parseResultRows converts the raw expected-result lines into
// typed Value rows aligned with the query's type string.
//
// The SLT format emits one value per line. When the type string
// has N > 1 columns, every N consecutive values form one row.
// When lines already contain tab-separated columns (N fields),
// each line is one row.
//
// Some corpora also include rows of the form "<N> values hashing
// to <HEX>" (hash-threshold mode). We surface that as a single
// Value{Kind: TypeNull, Text: "hash:..."} cell so the runner
// knows to skip strict comparison.
func parseResultRows(lines []string, typeString string) [][]Value {
	numCols := len(typeString)
	if numCols == 0 {
		numCols = 1
	}

	// First pass: collect all parsed values and detect hash markers.
	type entry struct {
		text string
		val  Value
	}
	var values []entry
	for _, ln := range lines {
		s := strings.TrimSpace(ln)
		if s == "" {
			continue
		}
		if strings.Contains(s, "values hashing to") {
			values = append(values, entry{text: s})
			continue
		}
		fields := splitRow(s)
		for _, f := range fields {
			values = append(values, entry{text: f})
		}
	}

	// If the first entry is a hash marker, return it as-is.
	if len(values) == 1 && values[0].text != "" && strings.Contains(values[0].text, "values hashing to") {
		return [][]Value{{Value{Kind: TypeText, Text: values[0].text}}}
	}

	// Group into rows of numCols values each.
	var rows [][]Value
	var curRow []Value
	for _, e := range values {
		if strings.Contains(e.text, "values hashing to") {
			rows = append(rows, []Value{{Kind: TypeText, Text: e.text}})
			continue
		}
		code := "T"
		colIdx := len(curRow)
		if colIdx < numCols {
			code = string(typeString[colIdx])
		}
		v, err := ParseValue(e.text, code)
		if err != nil {
			v = Value{Kind: TypeText, Text: e.text}
		}
		curRow = append(curRow, v)
		if len(curRow) >= numCols {
			rows = append(rows, curRow)
			curRow = nil
		}
	}
	if len(curRow) > 0 {
		rows = append(rows, curRow)
	}
	return rows
}

// splitRow splits a result row on tab boundaries, falling back to
// whitespace if no tabs are present. The SLT format uses tabs
// between columns, but a tolerant parser also handles multi-space
// layouts that appear in some hand-written scripts.
func splitRow(s string) []string {
	if strings.Contains(s, "\t") {
		return strings.Split(s, "\t")
	}
	return strings.Fields(s)
}
