package service

import (
	"sort"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func (s *Service) CreateCellar(input CreateCellarInput) (domain.Cellar, error) {
	now := s.clock.Now()
	cellar := domain.Cellar{
		Name:      input.Name,
		State:     domain.CellarActive,
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	cellar.Normalize()
	if err := cellar.Validate(); err != nil {
		return domain.Cellar{}, err
	}
	id, err := generateEntityID("cellar")
	if err != nil {
		return domain.Cellar{}, domain.NewError(domain.CodeStorage, err.Error())
	}
	cellar.ID = id
	nameKey := domain.NameKey(cellar.Name)

	err = s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, exists := snapshot.CellarIndex[nameKey]; exists {
			return domain.NewError(domain.CodeCellarNameConflict,
				"a cellar with the same normalized name already exists")
		}
		snapshot.Cellars[cellar.ID] = cellar
		snapshot.CellarIndex[nameKey] = cellar.ID
		return nil
	})
	if err != nil {
		return domain.Cellar{}, err
	}
	return cellar, nil
}

func (s *Service) GetCellar(id string) (domain.Cellar, error) {
	var found domain.Cellar
	err := s.store.View(func(snapshot domain.Snapshot) error {
		cellar, err := snapshotCellar(&snapshot, id)
		if err != nil {
			return err
		}
		found = cellar
		return nil
	})
	if err != nil {
		return domain.Cellar{}, err
	}
	return found, nil
}

func (s *Service) ListCellars() ([]domain.Cellar, error) {
	var cellars []domain.Cellar
	err := s.store.View(func(snapshot domain.Snapshot) error {
		cellars = make([]domain.Cellar, 0, len(snapshot.Cellars))
		for _, cellar := range snapshot.Cellars {
			cellars = append(cellars, cellar)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(cellars, func(i, j int) bool {
		if cellars[i].Name == cellars[j].Name {
			return cellars[i].ID < cellars[j].ID
		}
		return cellars[i].Name < cellars[j].Name
	})
	return cellars, nil
}

// ArchiveCellar freezes a cellar once it holds no in-flight work. Open runs
// or vessels still bound to a run keep the cellar active so archiving can
// never strand a half-finished product inside a frozen namespace.
func (s *Service) ArchiveCellar(id string, input VersionInput) (domain.Cellar, error) {
	var updated domain.Cellar
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		cellar, err := snapshotCellar(snapshot, id)
		if err != nil {
			return err
		}
		if err := s.checkExpected(cellar.Version, input.ExpectedVersion); err != nil {
			return err
		}
		for _, run := range snapshot.Runs {
			if run.CellarID == id && run.State != domain.RunCompleted &&
				run.State != domain.RunAborted {
				return domain.NewError(domain.CodeCellarInUse,
					"cellar has runs that are still open")
			}
		}
		for _, vessel := range snapshot.Vessels {
			if vessel.CellarID == id && vessel.ActiveRunID != nil {
				return domain.NewError(domain.CodeCellarInUse,
					"cellar has vessels still associated with a run")
			}
		}
		if err := cellar.Archive(s.clock.Now()); err != nil {
			return err
		}
		snapshot.Cellars[id] = cellar
		updated = cellar
		return nil
	})
	if err != nil {
		return domain.Cellar{}, err
	}
	return updated, nil
}

// ExportCellar returns the complete contents of one cellar namespace. Only
// entities whose CellarID matches are included, so identical codes in other
// cellars can never leak into the export.
func (s *Service) ExportCellar(id string) (CellarExport, error) {
	var export CellarExport
	err := s.store.View(func(snapshot domain.Snapshot) error {
		cellar, err := snapshotCellar(&snapshot, id)
		if err != nil {
			return err
		}
		export = CellarExport{
			Cellar:   cellar,
			Formulas: []domain.Formula{},
			Vessels:  []domain.Vessel{},
			Runs:     []domain.FermentationRun{},
		}
		for _, formula := range snapshot.Formulas {
			if formula.CellarID == id {
				export.Formulas = append(export.Formulas, formula.Clone())
			}
		}
		for _, vessel := range snapshot.Vessels {
			if vessel.CellarID == id {
				export.Vessels = append(export.Vessels, vessel)
			}
		}
		for _, run := range snapshot.Runs {
			if run.CellarID == id {
				export.Runs = append(export.Runs, run.Clone())
			}
		}
		return nil
	})
	if err != nil {
		return CellarExport{}, err
	}
	sort.Slice(export.Formulas, func(i, j int) bool {
		if export.Formulas[i].Name == export.Formulas[j].Name {
			return export.Formulas[i].ID < export.Formulas[j].ID
		}
		return export.Formulas[i].Name < export.Formulas[j].Name
	})
	sort.Slice(export.Vessels, func(i, j int) bool {
		if export.Vessels[i].Code == export.Vessels[j].Code {
			return export.Vessels[i].ID < export.Vessels[j].ID
		}
		return export.Vessels[i].Code < export.Vessels[j].Code
	})
	sort.Slice(export.Runs, func(i, j int) bool {
		if export.Runs[i].Code == export.Runs[j].Code {
			return export.Runs[i].ID < export.Runs[j].ID
		}
		return export.Runs[i].Code < export.Runs[j].Code
	})
	return export, nil
}
