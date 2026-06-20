// Package ENG defines shared error sentinels for the storage engine
// layer. Both LS and TB import from here to avoid silent divergence.
package ENG

import "errors"

// Catalog error sentinels shared by LS and TB.
var (
	ErrCatalogCorrupt  = errors.New("catalog: data corrupt")
	ErrUpgradeRequired = errors.New("catalog: schema version newer than supported")
	ErrCatalogNotFound = errors.New("catalog: table not found")
	ErrCatalogExists   = errors.New("catalog: table already exists")
	ErrCatalogClosed   = errors.New("catalog: closed")
)
