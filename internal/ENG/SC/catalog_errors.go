package sc

import "errors"

// Catalog error sentinels shared by LS and TB. Both packages
// re-export these to preserve their existing call sites.
// REQ000653.
var (
	ErrCatalogCorrupt  = errors.New("catalog: data corrupt")
	ErrUpgradeRequired = errors.New("catalog: schema version newer than supported")
	ErrCatalogNotFound = errors.New("catalog: table not found")
	ErrCatalogExists   = errors.New("catalog: table already exists")
	ErrCatalogClosed   = errors.New("catalog: closed")
)
