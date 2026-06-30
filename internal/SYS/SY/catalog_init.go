package SY

import (
	"path/filepath"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// openCatalog opens the system catalog and rehydrates the EX
// layer's in-memory schema registry from it. Called from
// Engine.open after the SQL executor is constructed.
// The catalog lives at <db-root>/catalog/ and owns a single
// catalog.dat file. Bootstrap is fail-fast: a corrupt or
// future-versioned file propagates the error and Open returns it
// to the caller, which is the only safe behavior — the user must
// not be allowed to start a database whose schema metadata is
// unreadable.
func (e *Engine) openCatalog() error {
	catDir := filepath.Join(e.dir, "catalog")
	cat, err := ls.NewCatalog(catDir)
	if err != nil {
		return err
	}
	// Wire the catalog into the EX layer so that subsequent
	// CREATE TABLE / DROP TABLE calls persist to it.
	DT.SetCatalog(cat)
	// Rehydrate the in-memory schema maps from the catalog so
	// queries can run against tables created in a previous
	// process. RegisterFromCatalog is idempotent.
	for _, entry := range cat.List() {
		if err := DT.RegisterFromCatalog(entry); err != nil {
			_ = cat.Close()
			return err
		}
	}
	e.catalog = cat
	return nil
}

// closeCatalog closes the system catalog. Idempotent and safe to
// call after openCatalog failed.
func (e *Engine) closeCatalog() {
	if e.catalog == nil {
		return
	}
	_ = e.catalog.Close()
	e.catalog = nil
	DT.SetCatalog(nil)
}
