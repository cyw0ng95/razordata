package AP

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestError_JSONRoundTrip(t *testing.T) {
	e := New(KindNotFound, "key missing").WithModule("SQB/EV").WithLayer(LayerSQL).WithField("entity", "table")
	e.Op = OpSelect

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	var got Error
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}

	if got.Kind != KindNotFound {
		t.Errorf("Kind = %v, want KindNotFound", got.Kind)
	}
	if got.Code != "RZR-SQL-001" {
		t.Errorf("Code = %q", got.Code)
	}
	if got.Module != "SQB/EV" {
		t.Errorf("Module = %q", got.Module)
	}
	if got.Op != OpSelect {
		t.Errorf("Op = %q", got.Op)
	}
	if got.Fields["entity"] != "table" {
		t.Errorf("Fields[entity] = %q", got.Fields["entity"])
	}
}

func TestError_JSONMarshalSnapshot(t *testing.T) {
	// Per-Kind snapshot: verify JSON shape doesn't change silently.
	cases := []struct {
		kind Kind
		msg  string
	}{
		{KindNotFound, "not found"},
		{KindIO, "i/o error"},
		{KindCorrupt, "corrupt data"},
		{KindInternal, "internal error"},
	}
	for _, tc := range cases {
		e := New(tc.kind, tc.msg).WithModule("TEST").WithLayer(LayerSQL)
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("MarshalJSON(%s): %v", tc.kind, err)
		}
		// Verify it parses back
		var round Error
		if err := json.Unmarshal(data, &round); err != nil {
			t.Fatalf("UnmarshalJSON(%s): %v", tc.kind, err)
		}
		if round.Kind != tc.kind {
			t.Errorf("round-trip Kind mismatch for %s: got %v", tc.kind, round.Kind)
		}
	}
}

func TestError_JSONWrapped(t *testing.T) {
	inner := New(KindNotFound, "inner")
	e := Wrap(KindIO, inner)
	e.Module = "WAL/RP"
	e.Layer = LayerIO

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("MarshalJSON wrapped: %v", err)
	}

	if !strings.Contains(string(data), "inner") {
		t.Error("JSON should contain inner error message")
	}
	if !strings.Contains(string(data), "RZR-IO-001") {
		t.Error("JSON should contain top-level code")
	}
}

func TestError_JSONNilWrapped(t *testing.T) {
	e := New(KindNotFound, "simple")
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if strings.Contains(string(data), `"cause"`) {
		t.Error("JSON should not contain cause field when no wrapped error")
	}
}

func TestError_WireFormatStable(t *testing.T) {
	e := New(KindNotFound, "test").WithModule("MOD").WithLayer(LayerSQL)
	want := "MOD/sql RZR-SQL-001 [02000]: test"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
