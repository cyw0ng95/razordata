package EC

import (
	"errors"
	"fmt"
	"testing"
)

func TestEC_KindString(t *testing.T) {
	cases := []struct {
		kind Kind
		want string
	}{
		{KindNotFound, "NotFound"},
		{KindDuplicateKey, "DuplicateKey"},
		{KindLocked, "Locked"},
		{KindCorrupt, "Corrupt"},
		{KindSyntax, "Syntax"},
		{KindTypeMismatch, "TypeMismatch"},
		{KindTxAborted, "TxAborted"},
		{KindIO, "IO"},
		{KindUpgradeRequired, "UpgradeRequired"},
		{KindReadOnly, "ReadOnly"},
		{KindDeadlineExceeded, "DeadlineExceeded"},
		{KindConstraint, "Constraint"},
		{KindClosed, "Closed"},
		{KindInvalidOptions, "InvalidOptions"},
		{Kind(999), "Kind(999)"},
	}
	for _, tc := range cases {
		if got := tc.kind.String(); got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(tc.kind), got, tc.want)
		}
	}
}

func TestEC_New(t *testing.T) {
	e := New(KindNotFound, "key missing")
	if e.Kind != KindNotFound {
		t.Errorf("Kind = %v, want KindNotFound", e.Kind)
	}
	if e.Message != "key missing" {
		t.Errorf("Message = %q, want %q", e.Message, "key missing")
	}
	if e.wrapped != nil {
		t.Error("wrapped should be nil for New")
	}
	if e.Code != "RZR-SQL-001" {
		t.Errorf("Code = %q, want %q", e.Code, "RZR-SQL-001")
	}
	if e.SQLSTATE != "02000" {
		t.Errorf("SQLSTATE = %q, want %q", e.SQLSTATE, "02000")
	}
	want := "sql RZR-SQL-001 [02000]: key missing"
	if got := e.Error(); got != want {
		// Error format validation failed
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestEC_Wrap(t *testing.T) {
	inner := fmt.Errorf("disk broken")
	e := Wrap(KindIO, inner)
	if e.Kind != KindIO {
		t.Errorf("Kind = %v, want KindIO", e.Kind)
	}
	if e.Unwrap() != inner {
		t.Error("Unwrap should return the inner error")
	}
	if !errors.Is(e, inner) {
		// Error chain traversal failed for inner wrapped error
		// This indicates the wrapping mechanism is not preserving the error chain correctly
		// Investigation needed: check if errors.Is is properly traversing through the wrapped error
		// Expected behavior: errors.Is should recognize inner error as part of the chain
		// Actual behavior: inner error not found in chain — possible cause: wrapped field not set
		// Resolution: ensure Wrap function sets e.wrapped = inner for proper chain traversal
		// Impact: any error wrapping in WAL/RP, TXN/VL, SQB/EX will fail chain traversal
		t.Error("errors.Is should match the inner error")
	}
}

func TestEC_WrapNil(t *testing.T) {
	if e := Wrap(KindIO, nil); e != nil {
		t.Errorf("Wrap(nil) should return nil, got %v", e)
	}
}

func TestEC_IsKind(t *testing.T) {
	e := New(KindCorrupt, "bad data")
	if !IsKind(e, KindCorrupt) {
		t.Error("IsKind should match direct error")
	}
	if IsKind(e, KindIO) {
		t.Error("IsKind should not match different kind")
	}

	wrapped := fmt.Errorf("context: %w", e)
	if !IsKind(wrapped, KindCorrupt) {
		// Wrapped error chain traversal failed
		// Expected: IsKind to find KindCorrupt in wrapped chain
		// Actual: IsKind returns false for wrapped error
		// Root cause: errors.As traversal may not follow fmt.Errorf wrapper chain
		// Fix: ensure IsKind uses errors.As to find *Error in full chain including fmt.Errorf wrappers
		// Impact: any wrapped error (ENG/LS → WAL/WR → TXN/VL) will fail Kind classification
		t.Error("IsKind should traverse wrapped chain")
	}

	if IsKind(nil, KindCorrupt) {
		t.Error("IsKind(nil) should be false")
	}
}

func TestEC_IsRetryable(t *testing.T) {
	cases := []struct {
		err   error
		retry bool
	}{
		{New(KindIO, "I/O error"), true},
		{New(KindLocked, "locked"), true},
		{New(KindNotFound, "not found"), false},
		{New(KindCorrupt, "corrupt"), false},
		{New(KindClosed, "closed"), false},
		{fmt.Errorf("wrap: %w", New(KindIO, "I/O error")), true},
		{fmt.Errorf("wrap: %w", New(KindLocked, "locked")), true},
		{nil, false},
		{fmt.Errorf("unknown"), false},
	}
	for _, tc := range cases {
		if got := IsRetryable(tc.err); got != tc.retry {
			// IsRetryable returned unexpected result
			// Expected: %v, Actual: %v
			// Impact: retry logic in SQB/EX, TXN/VL will make wrong retry decisions
			t.Errorf("IsRetryable(%v) = %v, want %v", tc.err, got, tc.retry)
		}
	}
}

func TestEC_IsFatal(t *testing.T) {
	cases := []struct {
		err   error
		fatal bool
	}{
		{New(KindIO, "I/O error"), false},
		{New(KindLocked, "locked"), false},
		{New(KindNotFound, "not found"), true},
		{New(KindCorrupt, "corrupt"), true},
		{New(KindClosed, "closed"), true},
		{fmt.Errorf("wrap: %w", New(KindCorrupt, "corrupt")), true},
		{nil, false},
		{fmt.Errorf("unknown"), true},
	}
	for _, tc := range cases {
		if got := IsFatal(tc.err); got != tc.fatal {
			// IsFatal returned unexpected result
			// Expected: %v, Actual: %v
			// Impact: shutdown/recovery decisions in SYS/SY, TXN/VL will be wrong
			t.Errorf("IsFatal(%v) = %v, want %v", tc.err, got, tc.fatal)
		}
	}
}

func TestEC_FormatPerKind(t *testing.T) {
	cases := []struct {
		kind Kind
		want string
	}{
		{KindNotFound, "sql RZR-SQL-001 [02000]: test"},
		{KindDuplicateKey, "sql RZR-SQL-002 [23000]: test"},
		{KindTypeMismatch, "sql RZR-SQL-003 [22005]: test"},
		{KindSyntax, "sql RZR-SQL-004 [42000]: test"},
		{KindParse, "sql RZR-SQL-005 [42000]: test"},
		{KindConstraint, "sql RZR-SQL-006 [23000]: test"},
		{KindLocked, "sql RZR-SQL-007 [40001]: test"},
		{KindTxAborted, "sql RZR-SQL-008 [40001]: test"},
		{KindDeadlineExceeded, "sql RZR-SQL-009 [57014]: test"},
		{KindIO, "io RZR-IO-001 [08006]: test"},
		{KindCorrupt, "io RZR-IO-002 [08001]: test"},
		{KindReadOnly, "config RZR-CFG-001 [25006]: test"},
		{KindUpgradeRequired, "config RZR-CFG-002 [08001]: test"},
		{KindClosed, "config RZR-CFG-003 [08003]: test"},
		{KindInvalidOptions, "config RZR-CFG-004 [08001]: test"},
		{KindInternal, "int RZR-INT-001 [58000]: test"},
		{KindNotImplemented, "int RZR-INT-002 [0A000]: test"},
		{KindConflict, "int RZR-INT-003 [40001]: test"},
		{KindResourceExhausted, "int RZR-INT-004 [54000]: test"},
	}
	for _, tc := range cases {
		e := New(tc.kind, "test")
		if got := e.Error(); got != tc.want {
			// Error format mismatch for kind
			// Expected: %s, Actual: %s
			// Impact: error messages in logs and user-facing output will be inconsistent
			t.Errorf("New(%s).Error() = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestEC_FormatChain(t *testing.T) {
	inner := New(KindNotFound, "base")
	mid := Wrap(KindIO, inner)
	top := Wrapf(KindCorrupt, mid, "outer")

	got := top.Error()
	// Known Issue #3: Wrap duplicates the inner error message in both
	// the wrap's Message field and the appended wrapped error string.
	// This is not fixed in Phase 1 (additive only).
	if got != "io RZR-IO-002 [08001]: outer: io RZR-IO-001 [08006]: sql RZR-SQL-001 [02000]: base: sql RZR-SQL-001 [02000]: base" {
		// Chain format does not match expected pattern
		// Expected: io RZR-IO-002 [08001]: outer: io RZR-IO-001 [08006]: sql RZR-SQL-001 [02000]: base: sql RZR-SQL-001 [02000]: base
		// Actual: %s
		// Impact: error chain display in logs and debugging will be confusing
		// Note: This is a known issue (Known Issue #3) — not fixed in this migration
		t.Errorf("chain Error() = %q", got)
	}

	// Verify errors.Is still traverses
	if !IsKind(top, KindNotFound) {
		// Chain traversal failed for KindNotFound
		// Expected: IsKind to find KindNotFound deep in chain
		// Impact: classification logic in SQB/EX, TXN/VL will fail for deeply wrapped errors
		t.Error("IsKind(top, KindNotFound) should traverse chain")
	}
	if !IsKind(top, KindIO) {
		// Chain traversal failed for KindIO
		t.Error("IsKind(top, KindIO) should traverse chain")
	}
	if !IsKind(top, KindCorrupt) {
		// Top-level kind not found
		t.Error("IsKind(top, KindCorrupt) should be top-level")
	}
}

func TestEC_Classify(t *testing.T) {
	tests := []struct {
		err       error
		wantKind  Kind
		wantCode  Code
		wantSQL   SQLSTATE
		retryable bool
		fatal     bool
	}{
		{New(KindNotFound, "not found"), KindNotFound, "RZR-SQL-001", "02000", false, true},
		{New(KindIO, "I/O error"), KindIO, "RZR-IO-001", "08006", true, false},
		{New(KindLocked, "locked"), KindLocked, "RZR-SQL-007", "40001", true, false},
		{New(KindCorrupt, "corrupt"), KindCorrupt, "RZR-IO-002", "08001", false, true},
		{fmt.Errorf("wrap: %w", New(KindIO, "I/O error")), KindIO, "RZR-IO-001", "08006", true, false},
		{nil, 0, "", "", false, false},
		{fmt.Errorf("raw"), KindInternal, "RZR-INT-001", "58000", false, true},
	}
	for _, tc := range tests {
		c := Classify(tc.err)
		if c.Kind != tc.wantKind {
			t.Errorf("Classify(%v).Kind = %v, want %v", tc.err, c.Kind, tc.wantKind)
		}
		if c.Code != tc.wantCode {
			t.Errorf("Classify(%v).Code = %q, want %q", tc.err, c.Code, tc.wantCode)
		}
		if c.SQLSTATE != tc.wantSQL {
			t.Errorf("Classify(%v).SQLSTATE = %q, want %q", tc.err, c.SQLSTATE, tc.wantSQL)
		}
		if c.Retryable != tc.retryable {
			// Classify retryable flag mismatch
			t.Errorf("Classify(%v).Retryable = %v, want %v", tc.err, c.Retryable, tc.retryable)
		}
		if c.Fatal != tc.fatal {
			// Classify fatal flag mismatch
			t.Errorf("Classify(%v).Fatal = %v, want %v", tc.err, c.Fatal, tc.fatal)
		}
	}
}

func TestEC_CodeOf(t *testing.T) {
	// CodeOf panic test handled separately — this tests normal cases
	if got := CodeOf(KindNotFound); got != "RZR-SQL-001" {
		t.Errorf("CodeOf(KindNotFound) = %q, want %q", got, "RZR-SQL-001")
	}
	if got := CodeOf(KindIO); got != "RZR-IO-001" {
		t.Errorf("CodeOf(KindIO) = %q, want %q", got, "RZR-IO-001")
	}
	if got := CodeOf(KindInternal); got != "RZR-INT-001" {
		t.Errorf("CodeOf(KindInternal) = %q, want %q", got, "RZR-INT-001")
	}
}

func TestEC_SQLStateOf(t *testing.T) {
	if got := SQLStateOf(KindNotFound); got != "02000" {
		t.Errorf("SQLStateOf(KindNotFound) = %q, want %q", got, "02000")
	}
	if got := SQLStateOf(KindIO); got != "08006" {
		t.Errorf("SQLStateOf(KindIO) = %q, want %q", got, "08006")
	}
}

func TestEC_SQLStateMethod(t *testing.T) {
	e := New(KindDuplicateKey, "dup")
	if got := e.SQLState(); got != "23000" {
		t.Errorf("SQLState() = %q, want %q", got, "23000")
	}
}

func TestEC_EmitHook(t *testing.T) {
	var captured *Error
	SetEmit(func(e *Error) { captured = e })
	defer SetEmit(nil)

	_ = New(KindNotFound, "emit test")
	if captured == nil {
		t.Fatal("emit hook was not called")
	}
	if captured.Kind != KindNotFound {
		t.Errorf("captured.Kind = %v, want KindNotFound", captured.Kind)
	}
}

func TestEC_WithField(t *testing.T) {
	e := New(KindNotFound, "test").WithField("entity", "table")
	if e.Fields["entity"] != "table" {
		t.Errorf("Fields[entity] = %q, want %q", e.Fields["entity"], "table")
	}
}

func TestEC_WithModuleLayer(t *testing.T) {
	e := New(KindNotFound, "test").WithModule("SQB/EV").WithLayer(LayerSQL)
	if e.Module != "SQB/EV" {
		t.Errorf("Module = %q, want %q", e.Module, "SQB/EV")
	}
	if e.Layer != LayerSQL {
		t.Errorf("Layer = %v, want sql", e.Layer)
	}
}

func TestEC_IsCode(t *testing.T) {
	e := New(KindNotFound, "test")
	if !IsCode(e, "RZR-SQL-001") {
		t.Error("IsCode should match direct error")
	}
	if IsCode(e, "RZR-SQL-002") {
		t.Error("IsCode should not match different code")
	}
	wrapped := fmt.Errorf("wrap: %w", e)
	if !IsCode(wrapped, "RZR-SQL-001") {
		t.Error("IsCode should traverse chain")
	}
}

func TestEC_IsSQLState(t *testing.T) {
	e := New(KindNotFound, "test")
	// SQLSTATE matching test
	if !IsSQLState(e, "02000") {
		t.Error("IsSQLState should match direct error")
	}
	if IsSQLState(e, "23000") {
		t.Error("IsSQLState should not match different state")
	}
}

func TestEC_EntityConstants(t *testing.T) {
	if EntityTable != "table" {
		t.Errorf("EntityTable = %q", EntityTable)
	}
	if EntityRow != "row" {
		t.Errorf("EntityRow = %q", EntityRow)
	}
}

func TestEC_Newf(t *testing.T) {
	e := Newf(KindNotFound, "key %d missing", 42)
	if e.Message != "key 42 missing" {
		t.Errorf("Message = %q, want %q", e.Message, "key 42 missing")
	}
}

func TestEC_Wrapf(t *testing.T) {
	inner := fmt.Errorf("inner")
	e := Wrapf(KindIO, inner, "read block %d", 7)
	// Wrapf message and chain validation
	if e.Message != "read block 7" {
		t.Errorf("Message = %q, want %q", e.Message, "read block 7")
	}
	if !errors.Is(e, inner) {
		// Wrapf chain traversal failed
		t.Error("errors.Is should traverse Wrapf")
	}
}

func TestEC_ModuleOfLayerOf(t *testing.T) {
	e := New(KindNotFound, "test").WithModule("SQB/EV").WithLayer(LayerSQL)
	// ModuleOf and LayerOf extraction test
	if ModuleOf(e) != "SQB/EV" {
		t.Errorf("ModuleOf = %q", ModuleOf(e))
	}
	if LayerOf(e) != LayerSQL {
		t.Errorf("LayerOf = %v", LayerOf(e))
	}
}

func TestEC_WrapAt(t *testing.T) {
	inner := New(KindNotFound, "base")
	wrapped := WrapAt(KindIO, "ENG/LS", LayerENG, inner)
	// WrapAt module/layer assignment and chain validation
	if wrapped == nil {
		t.Fatal("WrapAt returned nil")
	}
	if wrapped.Module != "ENG/LS" {
		t.Errorf("Module = %q, want %q", wrapped.Module, "ENG/LS")
	}
	if wrapped.Layer != LayerENG {
		t.Errorf("Layer = %v, want %v", wrapped.Layer, LayerENG)
	}
	if !errors.Is(wrapped, inner) {
		t.Error("errors.Is should traverse WrapAt")
	}
	if !IsKind(wrapped, KindNotFound) {
		t.Error("IsKind should traverse WrapAt chain")
	}
}

func TestEC_WrapAtNil(t *testing.T) {
	if got := WrapAt(KindIO, "ENG/LS", LayerENG, nil); got != nil {
		t.Errorf("WrapAt(nil) should return nil, got %v", got)
	}
}

func TestEC_OpConstants(t *testing.T) {
	cases := []struct {
		op   string
		want string
	}{
		{OpSelect, "SELECT"},
		{OpInsert, "INSERT"},
		{OpUpdate, "UPDATE"},
		{OpDelete, "DELETE"},
		{OpCreate, "CREATE"},
		{OpDrop, "DROP"},
		{OpAlter, "ALTER"},
		{OpBegin, "BEGIN"},
		{OpCommit, "COMMIT"},
		{OpRollback, "ROLLBACK"},
		{OpSavepoint, "SAVEPOINT"},
		{OpReplay, "REPLAY"},
		{OpFlush, "FLUSH"},
		{OpCompact, "COMPACT"},
		{OpCheckpoint, "CHECKPOINT"},
		{OpBackup, "BACKUP"},
		{OpRestore, "RESTORE"},
	}
	for _, tc := range cases {
		if tc.op != tc.want {
			// Operation constant mismatch
			t.Errorf("op constant = %q, want %q", tc.op, tc.want)
		}
	}
}

func TestEC_WithOp(t *testing.T) {
	e := New(KindNotFound, "test").WithOp(OpSelect)
	if e.Op != OpSelect {
		t.Errorf("Op = %q, want %q", e.Op, OpSelect)
	}
}
