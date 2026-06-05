package SYS

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// TestAP_RetryableAndFatal — R04 retry classification.
func TestAP_RetryableAndFatal(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		retryable bool
		fatal     bool
	}{
		{"io", AP.ErrIO, true, false},
		{"locked", AP.ErrLocked, true, false},
		{"syntax", AP.ErrSyntax, false, true},
		{"corrupt", AP.ErrCorrupt, false, true},
		{"type_mismatch", AP.ErrTypeMismatch, false, true},
		{"tx_aborted", AP.ErrTxAborted, false, true},
		{"upgrade_required", AP.ErrUpgradeRequired, false, true},
		{"read_only", AP.ErrReadOnly, false, true},
		{"deadline", AP.ErrDeadlineExceeded, false, true},
		{"not_found", AP.ErrNotFound, false, true},
		{"duplicate_key", AP.ErrDuplicateKey, false, true},
		{"already_open", AP.ErrAlreadyOpen, false, true},
		{"not_open", AP.ErrNotOpen, false, true},
		{"closed", AP.ErrClosed, false, true},
		{"invalid_options", AP.ErrInvalidOptions, false, true},
		{"no_active_txn", AP.ErrNoActiveTxn, false, true},
		{"unknown_savepoint", AP.ErrUnknownSavepoint, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AP.IsRetryable(c.err); got != c.retryable {
				t.Errorf("IsRetryable(%v) = %v, want %v", c.err, got, c.retryable)
			}
			if got := AP.IsFatal(c.err); got != c.fatal {
				t.Errorf("IsFatal(%v) = %v, want %v", c.err, got, c.fatal)
			}
		})
	}
}

// TestAP_Retryable_WrappedError — wrapped errors still classify via
// errors.Is (per ap.go doc: "Wrapped errors are unwrapped via
// errors.Is").
func TestAP_Retryable_WrappedError(t *testing.T) {
	wrapped := fmt.Errorf("outer context: %w", AP.ErrIO)
	if !AP.IsRetryable(wrapped) {
		t.Errorf("IsRetryable should catch %%w-wrapped IO error")
	}
	properly := errors.Join(errors.New("context"), AP.ErrSyntax)
	if !AP.IsFatal(properly) {
		t.Errorf("errors.Join with ErrSyntax should be classified as fatal")
	}
	// Plain errors.New does not unwrap to the sentinel; this is by
	// design — only errors that explicitly chain the sentinel are
	// classified.
	plain := errors.New("context: " + AP.ErrIO.Error())
	if AP.IsRetryable(plain) {
		t.Errorf("IsRetryable should NOT match text-only error (use %%w or errors.Join)")
	}
}

// TestAP_OptionsDefaults — R02 default values match the constants.
func TestAP_OptionsDefaults(t *testing.T) {
	if AP.DefaultPageSize != 4096 {
		t.Errorf("DefaultPageSize = %d, want 4096", AP.DefaultPageSize)
	}
	if AP.DefaultMemTableSize != 64*1024*1024 {
		t.Errorf("DefaultMemTableSize = %d, want 64MB", AP.DefaultMemTableSize)
	}
	if AP.DefaultBufferPoolMB != 256 {
		t.Errorf("DefaultBufferPoolMB = %d, want 256", AP.DefaultBufferPoolMB)
	}
	if AP.DefaultWALSizeMB != 64 {
		t.Errorf("DefaultWALSizeMB = %d, want 64", AP.DefaultWALSizeMB)
	}
	if AP.DefaultMaxLevel != 7 {
		t.Errorf("DefaultMaxLevel = %d, want 7", AP.DefaultMaxLevel)
	}
}

// TestAP_Version — R10.
func TestAP_Version(t *testing.T) {
	if AP.Version == "" {
		t.Error("Version is empty")
	}
}

// TestAP_ResultAndRows — ensure the public struct types are usable.
func TestAP_ResultAndRows(t *testing.T) {
	r := AP.Result{RowsAffected: 5, LastInsertID: 42}
	if r.RowsAffected != 5 || r.LastInsertID != 42 {
		t.Errorf("Result fields lost: %+v", r)
	}
	rows := AP.Rows{Cols: []string{"a", "b"}, Types: []int{1, 2}}
	if len(rows.Cols) != 2 || len(rows.Types) != 2 {
		t.Errorf("Rows fields lost: %+v", rows)
	}
}
