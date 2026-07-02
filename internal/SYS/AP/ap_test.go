package AP

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// TestAP_RetryableAndFatal — R04 retry classification.
func TestAP_RetryableAndFatal(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		retryable bool
		fatal     bool
	}{
		{"io", New(KindIO, "I/O error"), true, false},
		{"locked", New(KindLocked, "locked"), true, false},
		{"syntax", New(KindSyntax, "syntax"), false, true},
		{"corrupt", New(KindCorrupt, "corrupt"), false, true},
		{"type_mismatch", New(KindTypeMismatch, "type mismatch"), false, true},
		{"tx_aborted", New(KindTxAborted, "aborted"), false, true},
		{"upgrade_required", New(KindUpgradeRequired, "upgrade"), false, true},
		{"read_only", New(KindReadOnly, "read-only"), false, true},
		{"deadline", New(KindDeadlineExceeded, "deadline"), false, true},
		{"not_found", New(KindNotFound, "not found"), false, true},
		{"duplicate_key", New(KindDuplicateKey, "dup"), false, true},
		{"already_open", New(KindInvalidOptions, "already open"), false, true},
		{"not_open", New(KindClosed, "not open"), false, true},
		{"closed", New(KindClosed, "closed"), false, true},
		{"invalid_options", New(KindInvalidOptions, "invalid"), false, true},
		{"no_active_txn", ErrNoActiveTxn, false, true},
		{"unknown_savepoint", ErrUnknownSavepoint, false, true},
		{"constraint", ErrConstraint, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsRetryable(c.err); got != c.retryable {
				t.Errorf("IsRetryable(%v) = %v, want %v", c.err, got, c.retryable)
			}
			if got := IsFatal(c.err); got != c.fatal {
				t.Errorf("IsFatal(%v) = %v, want %v", c.err, got, c.fatal)
			}
		})
	}
}

// TestAP_Retryable_WrappedError — wrapped errors still classify via
// errors.Is (per ap.go doc: "Wrapped errors are unwrapped via
// errors.Is").
func TestAP_Retryable_WrappedError(t *testing.T) {
	wrapped := fmt.Errorf("outer context: %w", New(KindIO, "I/O error"))
	if !IsRetryable(wrapped) {
		t.Errorf("IsRetryable should catch %%w-wrapped IO error")
	}
	properly := errors.Join(errors.New("context"), New(KindSyntax, "syntax"))
	if !IsFatal(properly) {
		t.Errorf("errors.Join with ErrSyntax should be classified as fatal")
	}
	// Plain errors.New does not unwrap to the sentinel; this is by
	// design — only errors that explicitly chain the sentinel are
	// classified.
	plain := errors.New("context: " + New(KindIO, "I/O error").Error())
	if IsRetryable(plain) {
		t.Errorf("IsRetryable should NOT match text-only error (use %%w or errors.Join)")
	}
}

// TestAP_OptionsDefaults — R02 default values match the constants.
func TestAP_OptionsDefaults(t *testing.T) {
	if DefaultPageSize != 4096 {
		t.Errorf("DefaultPageSize = %d, want 4096", DefaultPageSize)
	}
	if DefaultMemTableSize != 64*1024*1024 {
		t.Errorf("DefaultMemTableSize = %d, want 64MB", DefaultMemTableSize)
	}
	if DefaultBufferPoolMB != 256 {
		t.Errorf("DefaultBufferPoolMB = %d, want 256", DefaultBufferPoolMB)
	}
	if DefaultWALSizeMB != 64 {
		t.Errorf("DefaultWALSizeMB = %d, want 64", DefaultWALSizeMB)
	}
	if DefaultMaxLevel != 7 {
		t.Errorf("DefaultMaxLevel = %d, want 7", DefaultMaxLevel)
	}
}

// TestAP_Version — R10.
func TestAP_Version(t *testing.T) {
	if Version == "" {
		t.Error("Version is empty")
	}
}

// TestAP_ResultAndRows — ensure the public struct types are usable.
func TestAP_ResultAndRows(t *testing.T) {
	r := Result{RowsAffected: 5, LastInsertID: 42}
	if r.RowsAffected != 5 || r.LastInsertID != 42 {
		t.Errorf("Result fields lost: %+v", r)
	}
	rows := Rows{cols: []string{"a", "b"}, types: []LX.TokenType{1, 2}}
	if len(rows.Cols()) != 2 || len(rows.Types()) != 2 {
		t.Errorf("Rows fields lost: %+v", rows)
	}
}
