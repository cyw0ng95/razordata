package EX

import (
	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
)

// exCatalogReader adapts the planner's in-memory catalog to OC.CatalogReader.
type exCatalogReader struct {
	catalog map[string]*tableInfo
}

func (r *exCatalogReader) TableColumns(table string) []string {
	ti, ok := r.catalog[table]
	if !ok {
		return nil
	}
	cols := make([]string, len(ti.cols))
	for i, c := range ti.cols {
		cols[i] = c.Name
	}
	return cols
}

func (r *exCatalogReader) Indexes(table string) []string {
	ti, ok := r.catalog[table]
	if !ok {
		return nil
	}
	names := make([]string, 0, len(ti.indexes))
	for name := range ti.indexes {
		names = append(names, name)
	}
	return names
}

func (r *exCatalogReader) IndexColumns(table, index string) []string {
	ti, ok := r.catalog[table]
	if !ok {
		return nil
	}
	return ti.indexes[index]
}

func (r *exCatalogReader) TablePK(table string) string {
	ti, ok := r.catalog[table]
	if !ok {
		return ""
	}
	return ti.pk
}

// check interface satisfaction
var _ OC.CatalogReader = (*exCatalogReader)(nil)