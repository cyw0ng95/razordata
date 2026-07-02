package AP

import (
	"errors"
	"fmt"
	"testing"
)

func TestError_KindString(t *testing.T) {
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

func TestError_New(t *testing.T) {
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
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestError_Wrap(t *testing.T) {
	inner := fmt.Errorf("disk broken")
	e := Wrap(KindIO, inner)
	if e.Kind != KindIO {
		t.Errorf("Kind = %v, want KindIO", e.Kind)
	}
	if e.Unwrap() != inner {
		t.Error("Unwrap should return the inner error")
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should match the inner error")
	}
}

func TestError_WrapNil(t *testing.T) {
	if e := Wrap(KindIO, nil); e != nil {
		t.Errorf("Wrap(nil) should return nil, got %v", e)
	}
}

func TestIsKind(t *testing.T) {
	e := New(KindCorrupt, "bad data")
	if !IsKind(e, KindCorrupt) {
		t.Error("IsKind should match direct error")
	}
	if IsKind(e, KindIO) {
		t.Error("IsKind should not match different kind")
	}

	wrapped := fmt.Errorf("context: %w", e)
	if !IsKind(wrapped, KindCorrupt) {
		t.Error("IsKind should traverse wrapped chain")
	}

	if IsKind(nil, KindCorrupt) {
		t.Error("IsKind(nil) should be false")
	}
}

func TestIsKind_BackwardCompat(t *testing.T) {
	// Sentinels created via New should match with errors.Is
	if !errors.Is(ErrNotFound, ErrNotFound) {
		t.Error("errors.Is(ErrNotFound, ErrNotFound) should be true")
	}
	// Wrapped sentinel should still match
	wrapped := fmt.Errorf("wrap: %w", ErrNotFound)
	if !errors.Is(wrapped, ErrNotFound) {
		t.Error("errors.Is(wrapped, ErrNotFound) should be true")
	}
	if !IsKind(wrapped, KindNotFound) {
		t.Error("IsKind(wrapped, KindNotFound) should be true")
	}
}

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		err   error
		retry bool
	}{
		{ErrIO, true},
		{ErrLocked, true},
		{ErrNotFound, false},
		{ErrCorrupt, false},
		{ErrClosed, false},
		{fmt.Errorf("wrap: %w", ErrIO), true},
		{fmt.Errorf("wrap: %w", ErrLocked), true},
		{nil, false},
		{fmt.Errorf("unknown"), false},
	}
	for _, tc := range cases {
		if got := IsRetryable(tc.err); got != tc.retry {
			t.Errorf("IsRetryable(%v) = %v, want %v", tc.err, got, tc.retry)
		}
	}
}

func TestIsFatal(t *testing.T) {
	cases := []struct {
		err   error
		fatal bool
	}{
		{ErrIO, false},
		{ErrLocked, false},
		{ErrNotFound, true},
		{ErrCorrupt, true},
		{ErrClosed, true},
		{fmt.Errorf("wrap: %w", ErrCorrupt), true},
		{nil, false},
		{fmt.Errorf("unknown"), true},
	}
	for _, tc := range cases {
		if got := IsFatal(tc.err); got != tc.fatal {
			t.Errorf("IsFatal(%v) = %v, want %v", tc.err, got, tc.fatal)
		}
	}
}

func TestSentinel_KindMatch(t *testing.T) {
	// Every sentinel should have the correct Kind
	cases := []struct {
		err  *Error
		kind Kind
	}{
		{ErrNotFound, KindNotFound},
		{ErrDuplicateKey, KindDuplicateKey},
		{ErrLocked, KindLocked},
		{ErrCorrupt, KindCorrupt},
		{ErrSyntax, KindSyntax},
		{ErrTypeMismatch, KindTypeMismatch},
		{ErrTxAborted, KindTxAborted},
		{ErrIO, KindIO},
		{ErrUpgradeRequired, KindUpgradeRequired},
		{ErrReadOnly, KindReadOnly},
		{ErrDeadlineExceeded, KindDeadlineExceeded},
		{ErrAlreadyOpen, KindInvalidOptions},
		{ErrNotOpen, KindClosed},
		{ErrClosed, KindClosed},
		{ErrInvalidOptions, KindInvalidOptions},
		{ErrNoActiveTxn, KindConstraint},
		{ErrUnknownSavepoint, KindConstraint},
		{ErrConstraint, KindConstraint},
	}
	for _, tc := range cases {
		if tc.err.Kind != tc.kind {
			t.Errorf("%v.Kind = %v, want %v", tc.err, tc.err.Kind, tc.kind)
		}
		if !IsKind(tc.err, tc.kind) {
			t.Errorf("IsKind(%v, %v) = false", tc.err, tc.kind)
		}
	}
}

func TestError_FormatPerKind(t *testing.T) {
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
			t.Errorf("New(%s).Error() = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestError_FormatChain(t *testing.T) {
	inner := New(KindNotFound, "base")
	mid := Wrap(KindIO, inner)
	top := Wrapf(KindCorrupt, mid, "outer")

	got := top.Error()
	// Known Issue #3: Wrap duplicates the inner error message in both
	// the wrap's Message field and the appended wrapped error string.
	// This is not fixed in Phase 1 (additive only).
	if got != "io RZR-IO-002 [08001]: outer: io RZR-IO-001 [08006]: sql RZR-SQL-001 [02000]: base: sql RZR-SQL-001 [02000]: base" {
		t.Errorf("chain Error() = %q", got)
	}

	// Verify errors.Is still traverses
	if !IsKind(top, KindNotFound) {
		t.Error("IsKind(top, KindNotFound) should traverse chain")
	}
	if !IsKind(top, KindIO) {
		t.Error("IsKind(top, KindIO) should traverse chain")
	}
	if !IsKind(top, KindCorrupt) {
		t.Error("IsKind(top, KindCorrupt) should be top-level")
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		err        error
		wantKind   Kind
		wantCode   Code
		wantSQL    SQLSTATE
		retryable  bool
		fatal      bool
	}{
		{ErrNotFound, KindNotFound, "RZR-SQL-001", "02000", false, true},
		{ErrIO, KindIO, "RZR-IO-001", "08006", true, false},
		{ErrLocked, KindLocked, "RZR-SQL-007", "40001", true, false},
		{ErrCorrupt, KindCorrupt, "RZR-IO-002", "08001", false, true},
		{fmt.Errorf("wrap: %w", ErrIO), KindIO, "RZR-IO-001", "08006", true, false},
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
			t.Errorf("Classify(%v).Retryable = %v, want %v", tc.err, c.Retryable, tc.retryable)
		}
		if c.Fatal != tc.fatal {
			t.Errorf("Classify(%v).Fatal = %v, want %v", tc.err, c.Fatal, tc.fatal)
		}
	}
}

func TestCodeOf(t *testing.T) {
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

func TestSQLStateOf(t *testing.T) {
	if got := SQLStateOf(KindNotFound); got != "02000" {
		t.Errorf("SQLStateOf(KindNotFound) = %q, want %q", got, "02000")
	}
	if got := SQLStateOf(KindIO); got != "08006" {
		t.Errorf("SQLStateOf(KindIO) = %q, want %q", got, "08006")
	}
}

func TestError_SQLStateMethod(t *testing.T) {
	e := New(KindDuplicateKey, "dup")
	if got := e.SQLState(); got != "23000" {
		t.Errorf("SQLState() = %q, want %q", got, "23000")
	}
}

func TestLegacySentinel_StillUsable(t *testing.T) {
	if !errors.Is(ErrNotFound, ErrNotFound) {
		t.Error("errors.Is(ErrNotFound, ErrNotFound) should work")
	}
	if !IsKind(ErrNotFound, KindNotFound) {
		t.Error("IsKind(ErrNotFound, KindNotFound) should work")
	}
	wrapped := fmt.Errorf("wrap: %w", ErrNotFound)
	if !errors.Is(wrapped, ErrNotFound) {
		t.Error("errors.Is(wrapped, ErrNotFound) should work")
	}
}

func TestEmit_Hook(t *testing.T) {
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

func TestError_WithField(t *testing.T) {
	e := New(KindNotFound, "test").WithField("entity", "table")
	if e.Fields["entity"] != "table" {
		t.Errorf("Fields[entity] = %q, want %q", e.Fields["entity"], "table")
	}
}

func TestError_WithModuleLayer(t *testing.T) {
	e := New(KindNotFound, "test").WithModule("SQB/EV").WithLayer(LayerSQL)
	if e.Module != "SQB/EV" {
		t.Errorf("Module = %q, want %q", e.Module, "SQB/EV")
	}
	if e.Layer != LayerSQL {
		t.Errorf("Layer = %v, want sql", e.Layer)
	}
}

func TestIsCode(t *testing.T) {
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

func TestIsSQLState(t *testing.T) {
	e := New(KindNotFound, "test")
	if !IsSQLState(e, "02000") {
		t.Error("IsSQLState should match direct error")
	}
	if IsSQLState(e, "23000") {
		t.Error("IsSQLState should not match different state")
	}
}

func TestKindEntityConstants(t *testing.T) {
	if EntityTable != "table" {
		t.Errorf("EntityTable = %q", EntityTable)
	}
	if EntityRow != "row" {
		t.Errorf("EntityRow = %q", EntityRow)
	}
}

func TestNewf(t *testing.T) {
	e := Newf(KindNotFound, "key %d missing", 42)
	if e.Message != "key 42 missing" {
		t.Errorf("Message = %q, want %q", e.Message, "key 42 missing")
	}
}

func TestWrapf(t *testing.T) {
	inner := fmt.Errorf("inner")
	e := Wrapf(KindIO, inner, "read block %d", 7)
	if e.Message != "read block 7" {
		t.Errorf("Message = %q, want %q", e.Message, "read block 7")
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should traverse Wrapf")
	}
}

func TestModuleOfLayerOf(t *testing.T) {
	e := New(KindNotFound, "test").WithModule("SQB/EV").WithLayer(LayerSQL)
	if ModuleOf(e) != "SQB/EV" {
		t.Errorf("ModuleOf = %q", ModuleOf(e))
	}
	if LayerOf(e) != LayerSQL {
		t.Errorf("LayerOf = %v", LayerOf(e))
	}
}

func TestWrapAt(t *testing.T) {
	inner := New(KindNotFound, "base")
	wrapped := WrapAt(KindIO, "ENG/LS", LayerENG, inner)
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

func TestWrapAt_Nil(t *testing.T) {
	if got := WrapAt(KindIO, "ENG/LS", LayerENG, nil); got != nil {
		t.Errorf("WrapAt(nil) should return nil, got %v", got)
	}
}

func TestFromSentinel(t *testing.T) {
	wrapped := FromSentinel(ErrNotFound, "SYS/SE", LayerSQL)
	if wrapped == nil {
		t.Fatal("FromSentinel returned nil")
	}
	if wrapped.Kind != KindNotFound {
		t.Errorf("Kind = %v, want KindNotFound", wrapped.Kind)
	}
	if wrapped.Module != "SYS/SE" {
		t.Errorf("Module = %q", wrapped.Module)
	}
	if !errors.Is(wrapped, ErrNotFound) {
		t.Error("errors.Is should traverse FromSentinel")
	}
}

func TestFromSentinel_Nil(t *testing.T) {
	if got := FromSentinel(nil, "SYS/SE", LayerSQL); got != nil {
		t.Errorf("FromSentinel(nil) should return nil, got %v", got)
	}
}

func TestFromSentinel_Unknown(t *testing.T) {
	unknown := fmt.Errorf("unknown sentinel")
	wrapped := FromSentinel(unknown, "SYS/SE", LayerSQL)
	if wrapped == nil {
		t.Fatal("FromSentinel(unknown) returned nil")
	}
	if wrapped.Kind != KindInternal {
		t.Errorf("Kind = %v, want KindInternal for unknown", wrapped.Kind)
	}
}

func TestOp_AllConstants(t *testing.T) {
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
			t.Errorf("op constant = %q, want %q", tc.op, tc.want)
		}
	}
}

func TestError_WithOp(t *testing.T) {
	e := New(KindNotFound, "test").WithOp(OpSelect)
	if e.Op != OpSelect {
		t.Errorf("Op = %q, want %q", e.Op, OpSelect)
	}
}
