package EX

import (
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
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

// exSubPlanner adapts the planner to OC.SubPlanner. REQ001448.
type exSubPlanner struct {
	p *Planner
}

func (s *exSubPlanner) PlanSubquery(stmt PS.Stmt, outerAliases []string) (pl.Operator, error) {
	// Plan the subquery using the existing planner path.
	// The outerAliases hint is stored on Planner via SetOuterAliases so
	// the candidate-join-key algorithm avoids pulling columns from
	// outer tables. Returns the root operator.
	s.p.SetOuterAliases(outerAliases)
	defer s.p.SetOuterAliases(nil)
	result, err := s.p.Plan(stmt)
	if err != nil || result == nil {
		return nil, err
	}
	if result.Root == nil {
		return nil, nil
	}
	return result.Root.(pl.Operator), nil
}

var _ OC.SubPlanner = (*exSubPlanner)(nil)