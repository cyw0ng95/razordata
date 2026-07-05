package AP

// JSON tests moved to LOG/EC (internal/LOG/EC/error_json_test.go).
// Backward compatibility: verify AP.Error JSON marshaling works.
import (
	"encoding/json"
	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
	"strings"
	"testing"
)

func TestAP_ErrorJSONReExport(t *testing.T) {
	e := EC.New(EC.KindNotFound, "key missing").WithModule("TEST").WithLayer(EC.LayerSQL)

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	var got Error
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}

	if got.Kind != EC.KindNotFound {
		t.Errorf("Kind = %v, want KindNotFound", got.Kind)
	}
	if got.Code != "RZR-SQL-001" {
		t.Errorf("Code = %q", got.Code)
	}
	if !strings.Contains(string(data), "key missing") {
		t.Error("JSON should contain message")
	}
}
