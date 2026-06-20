package EX

import (
	"testing"
)

func TestJSON_Extract(t *testing.T) {
	tests := []struct {
		jsonStr string
		path    string
		want    any
	}{
		{`{"a": 1, "b": "hello"}`, "a", float64(1)},
		{`{"a": 1, "b": "hello"}`, "b", "hello"},
		{`{"a": {"b": 2}}`, "a.b", float64(2)},
		{`{"a": [1, 2, 3]}`, "a.1", float64(2)},
		{`{"a": 1}`, "c", nil},
		{`[1, 2, 3]`, "1", float64(2)},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			v, err := parseJSON(tt.jsonStr)
			if err != nil {
				t.Fatal(err)
			}
			got, err := jsonExtract(v, tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("jsonExtract(%s, %s) = %v, want %v", tt.jsonStr, tt.path, got, tt.want)
			}
		})
	}
}

func TestJSON_Type(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`"hello"`, "text"},
		{`123`, "real"},
		{`true`, "integer"},
		{`null`, "null"},
		{`[1,2]`, "array"},
		{`{"a":1}`, "object"},
	}
	for _, tt := range tests {
		v, err := parseJSON(tt.input)
		if err != nil {
			t.Fatal(err)
		}
		got := jsonType(v)
		if got != tt.want {
			t.Errorf("jsonType(%s) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

func TestJSON_Valid(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{`{"a": 1}`, true},
		{`[1, 2, 3]`, true},
		{`"hello"`, true},
		{`not json`, false},
		{`{bad`, false},
	}
	for _, tt := range tests {
		got := jsonValid(tt.input)
		if got != tt.want {
			t.Errorf("jsonValid(%s) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestJSON_Array(t *testing.T) {
	got, err := jsonArray([]any{int64(1), "two", float64(3.0)})
	if err != nil {
		t.Fatal(err)
	}
	want := `[1,"two",3]`
	if got != want {
		t.Errorf("jsonArray = %s, want %s", got, want)
	}
}

func TestJSON_Object(t *testing.T) {
	got, err := jsonObject([]any{"a", int64(1), "b", "two"})
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"a":1,"b":"two"}` {
		t.Errorf("jsonObject = %s", got)
	}
}

func TestJSON_Set(t *testing.T) {
	got, err := jsonSet(`{"a": 1}`, "a", float64(2))
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"a":2}` {
		t.Errorf("jsonSet = %s, want {\"a\":2}", got)
	}
}

func TestJSON_ArrowOperator(t *testing.T) {
	got, err := jsonArrowOperator(`{"name": "alice"}`, "name")
	if err != nil {
		t.Fatal(err)
	}
	if got != `"alice"` {
		t.Errorf("jsonArrowOperator = %s, want \"alice\"", got)
	}
}

func TestJSON_ArrowTextOperator(t *testing.T) {
	got, err := jsonArrowTextOperator(`{"name": "alice"}`, "name")
	if err != nil {
		t.Fatal(err)
	}
	if got != "alice" {
		t.Errorf("jsonArrowTextOperator = %v, want alice", got)
	}
}

func TestJSON_InvalidInput(t *testing.T) {
	_, err := parseJSON(`not json`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestJSON_NestedExtract(t *testing.T) {
	jsonStr := `{"a": {"b": {"c": "deep"}}}`
	v, _ := parseJSON(jsonStr)
	got, err := jsonExtract(v, "a.b.c")
	if err != nil {
		t.Fatal(err)
	}
	if got != "deep" {
		t.Errorf("nested extract = %v, want deep", got)
	}
}

func TestJSON_EvalFunc(t *testing.T) {
	tests := []struct {
		name string
		args []any
		want any
	}{
		{"JSON_VALID", []any{`{"a":1}`}, int64(1)},
		{"JSON_VALID", []any{`bad`}, int64(0)},
		{"JSON_TYPE", []any{`"hi"`}, "text"},
		{"JSON_TYPE", []any{`[1]`}, "array"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evalJSONFunc(tt.name, tt.args)
			if err != nil {
				t.Fatalf("%s error: %v", tt.name, err)
			}
			if got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
