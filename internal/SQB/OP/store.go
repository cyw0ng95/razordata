// Package OP: backward-compat aliases for storage helpers that have
// moved to DT. The functions live in DT/storage.go now; the OP-side
// aliases remain so existing OP-internal callers and EX-side var
// aliases continue to work without source changes.
//
// REQ: Split the storage codec out of the SQB/OP layer into DT so
// downstream packages (UT, future WT) can reach it without pulling
// the operator type graph.
package OP

import (
	"errors"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// Store and StatsCatalog are now in DT.
type Store = DT.Store
type StatsCatalog = DT.StatsCatalog

// ErrNoEngine is returned when a query requires a wired store but the
// executor was constructed without one.
var ErrNoEngine = errors.New("op: no engine wired; use NewExecutorWithEngine")
var ErrTableNotRegisteredForStorage = DT.ErrTableNotRegisteredForStorage
var ErrNoPKForStorage = DT.ErrNoPKForStorage

// Type aliases for schema types now in DT.
type StoreSchema = DT.StoreSchema
type UniqueKey = DT.UniqueKey
type ForeignKeyConstraint = DT.ForeignKeyConstraint
type RegisteredIndex = DT.RegisteredIndex

// Backward-compat aliases. The implementations live in DT/storage.go.
var (
	TablePrefix             = DT.TablePrefix
	EncodeTablePrefix       = DT.EncodeTablePrefix
	EncodeRow               = DT.EncodeRow
	DecodeRow               = DT.DecodeRow
	RowKey                  = DT.RowKey
	ExtractPK               = DT.ExtractPK
	ExtractPKForUpdate      = DT.ExtractPKForUpdate
	MaintainIndexesOnInsert = DT.MaintainIndexesOnInsert
	MaintainIndexesOnDelete = DT.MaintainIndexesOnDelete
	MaintainIndexesOnUpdate = DT.MaintainIndexesOnUpdate
	BuildIndexKey           = DT.BuildIndexKey
	IndexValueFromKey       = DT.IndexValueFromKey
)
