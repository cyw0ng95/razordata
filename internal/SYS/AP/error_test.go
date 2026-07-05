package AP

// Tests have been moved to LOG/EC (internal/LOG/EC/*_test.go).
// This file is retained for backward compatibility.
//
// Re-exported types from LOG/EC are tested in LOG/EC directly.
//
// Backward compatibility test: verify that AP.Error is the same type as EC.Error.
import (
	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
	"testing"
)

func TestAP_ErrorReExport(t *testing.T) {
	// Verify that AP.Error is the same type as EC.Error
	var apErr *Error
	apErr = EC.New(EC.KindNotFound, "test")
	if apErr.Kind != EC.KindNotFound {
		t.Errorf("AP.Error.Kind mismatch after EC.New: got %v, want KindNotFound", apErr.Kind)
	}

	// Verify AP.Kind == EC.Kind
	var apKind Kind
	apKind = EC.KindIO
	if apKind != EC.KindIO {
		t.Errorf("AP.Kind != EC.Kind: got %v, want %v", apKind, EC.KindIO)
	}

	// Verify AP.IsKind works on EC errors
	ecErr := EC.New(EC.KindIO, "test")
	if !IsKind(ecErr, EC.KindIO) {
		t.Error("AP.IsKind should work on EC.Error")
	}
}
