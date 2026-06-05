// Package SC defines the schema cluster for the storage engine: column
// types, table definitions, and row validation. The types and the
// validator are extracted from ENG/LS in iter-10; the package is the
// single source of truth for schema definition and constraint checking.
//
// This file was moved from ENG/LS/schema.go in iter-10 (Phase 0).
// Behavior is unchanged from the original v1 implementation; only the
// package path differs.
package sc

import (
	"errors"
)

var (
	ErrNullValue    = errors.New("null value not allowed")
	ErrTypeMismatch = errors.New("type mismatch")
	ErrConstraint   = errors.New("constraint violation")
	ErrInvalidValue = errors.New("invalid value")
)

// Row is a logical row whose values are already encoded as raw bytes
// per their column type (see EncodeInt, EncodeFloat, etc. in the DP
// package). A nil entry in Values means NULL.
type Row struct {
	Values [][]byte
}

// TableSchema describes a table: a stable tableID, a logical name, an
// ordered list of column definitions, and the column indices that
// form the primary key. A zero PrimaryKey slice means the table has
// no primary key (used by the v1 in-memory tables; iter-10's TB
// catalog assigns a tableID even to PK-less tables).
type TableSchema struct {
	TableID    uint64
	Name       string
	Columns    []ColumnDef
	PrimaryKey []int
}

// ColumnDef describes a single column: its name, type, nullability,
// optional default value (already encoded as raw bytes), and whether
// it is part of the primary key.
type ColumnDef struct {
	Name       string
	Type       ColumnType
	Nullable   bool
	Default    []byte
	PrimaryKey bool
}

// ColumnType enumerates the column types supported by the v1 schema.
// Values are stable and persisted; do not renumber.
type ColumnType uint8

const (
	CTInt       ColumnType = 0
	CTBigInt    ColumnType = 1
	CTVarchar   ColumnType = 2
	CTFloat     ColumnType = 3
	CTBool      ColumnType = 4
	CTText      ColumnType = 5
	CTBlob      ColumnType = 6
	CTTimestamp ColumnType = 7
)

// Validator applies the table's schema to a row. Construction is via
// NewValidator; the zero value is not usable (the methods are defined
// on the pointer receiver for future-proofing if state is ever added).
type Validator struct{}

// NewValidator returns a ready-to-use validator.
func NewValidator() *Validator {
	return &Validator{}
}

// ValidateRow checks that row matches schema: every column has a
// value of the right shape, and every non-nullable column has a
// non-nil value. It panics if len(row.Values) < len(schema.Columns)
// (this matches the v1 behavior and is locked in by the existing
// TestValidateRow_WrongColumnCount). For a non-panicking variant,
// callers should check the length themselves before calling.
func (v *Validator) ValidateRow(row Row, schema *TableSchema) error {
	for i, col := range schema.Columns {
		val := row.Values[i]

		if val == nil {
			if !col.Nullable {
				return ErrNullValue
			}
			continue
		}

		if err := v.ValidateType(val, col.Type); err != nil {
			return err
		}

		if err := v.ValidateConstraints(val, &col); err != nil {
			return err
		}
	}

	return nil
}

// ValidateType checks that val is the right shape for colType. Fixed-
// width types must be exactly the expected length; variable-width
// types are accepted at any length. A nil val is accepted (the NULL
// check is the caller's responsibility).
func (v *Validator) ValidateType(val []byte, colType ColumnType) error {
	if val == nil {
		return nil
	}

	switch colType {
	case CTInt:
		if len(val) != 8 {
			return ErrTypeMismatch
		}
	case CTBigInt:
		if len(val) != 8 {
			return ErrTypeMismatch
		}
	case CTFloat:
		if len(val) != 8 {
			return ErrTypeMismatch
		}
	case CTBool:
		if len(val) != 1 {
			return ErrTypeMismatch
		}
	case CTVarchar, CTText:
	case CTBlob:
	case CTTimestamp:
		if len(val) != 8 {
			return ErrTypeMismatch
		}
	default:
		return ErrInvalidValue
	}

	return nil
}

// ValidateConstraints enforces per-column constraints. The v1
// implementation only recognizes the column's Default value: a row
// value equal to the default is considered a no-op (returns nil).
// Other DEFAULT semantics (NOT NULL is already handled by
// ValidateRow, PRIMARY KEY uniqueness is not yet enforced here) are
// future work; see ROADMAP v1.1 #4 (NOT NULL/DEFAULT enforcement).
func (v *Validator) ValidateConstraints(val []byte, col *ColumnDef) error {
	if col.Default != nil && string(val) == string(col.Default) {
		return nil
	}

	return nil
}

// CompareColumnDef reports whether two column definitions are equal
// across the four fields that matter for schema versioning: name,
// type, nullability, and primary-key membership. Default values are
// not part of the comparison (they are values, not structure).
func (v *Validator) CompareColumnDef(a, b ColumnDef) bool {
	if a.Name != b.Name {
		return false
	}
	if a.Type != b.Type {
		return false
	}
	if a.Nullable != b.Nullable {
		return false
	}
	if a.PrimaryKey != b.PrimaryKey {
		return false
	}
	return true
}
