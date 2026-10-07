package service

import (
	"sort"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func (s *Service) CreateFormula(
	cellarID string,
	input CreateFormulaInput,
) (domain.Formula, error) {
	now := s.clock.Now()
	formula := domain.Formula{
		CellarID:              cellarID,
		Name:                  input.Name,
		Style:                 input.Style,
		TargetOriginalGravity: input.TargetOriginalGravity,
		TargetFinalGravity:    input.TargetFinalGravity,
		MaxFermentationTempC:  input.MaxFermentationTempC,
		MinimumDays:           input.MinimumDays,
		MaximumDays:           input.MaximumDays,
		Steps:                 []domain.FormulaStep{},
		State:                 domain.FormulaDraft,
		Version:               1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	formula.Normalize()
	if err := formula.Validate(); err != nil {
		return domain.Formula{}, err
	}
	id, err := generateEntityID("formula")
	if err != nil {
		return domain.Formula{}, domain.NewError(domain.CodeStorage, err.Error())
	}
	formula.ID = id
	nameKey := domain.ScopedIndexKey(cellarID, domain.NameKey(formula.Name))

	err = s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, err := writableCellar(snapshot, cellarID); err != nil {
			return err
		}
		if _, exists := snapshot.NameIndex[nameKey]; exists {
			return domain.NewError(domain.CodeFormulaNameConflict,
				"a formula with the same normalized name already exists in this cellar")
		}
		snapshot.Formulas[formula.ID] = formula
		snapshot.NameIndex[nameKey] = formula.ID
		return nil
	})
	if err != nil {
		return domain.Formula{}, err
	}
	return formula.Clone(), nil
}

func (s *Service) SetFormulaSteps(
	cellarID string,
	id string,
	input SetFormulaStepsInput,
) (domain.Formula, error) {
	var updated domain.Formula
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, err := writableCellar(snapshot, cellarID); err != nil {
			return err
		}
		formula, err := cellarFormula(snapshot, cellarID, id)
		if err != nil {
			return err
		}
		if err := s.checkExpected(formula.Version, input.ExpectedVersion); err != nil {
			return err
		}
		if err := formula.SetSteps(input.Steps); err != nil {
			return err
		}
		formula.UpdatedAt = s.clock.Now()
		snapshot.Formulas[id] = formula
		updated = formula.Clone()
		return nil
	})
	if err != nil {
		return domain.Formula{}, err
	}
	return updated, nil
}

func (s *Service) ApproveFormula(
	cellarID string,
	id string,
	input VersionInput,
) (domain.Formula, error) {
	var updated domain.Formula
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, err := writableCellar(snapshot, cellarID); err != nil {
			return err
		}
		formula, err := cellarFormula(snapshot, cellarID, id)
		if err != nil {
			return err
		}
		if err := s.checkExpected(formula.Version, input.ExpectedVersion); err != nil {
			return err
		}
		if err := formula.Approve(s.clock.Now()); err != nil {
			return err
		}
		snapshot.Formulas[id] = formula
		updated = formula.Clone()
		return nil
	})
	if err != nil {
		return domain.Formula{}, err
	}
	return updated, nil
}

func (s *Service) RetireFormula(
	cellarID string,
	id string,
	input VersionInput,
) (domain.Formula, error) {
	var updated domain.Formula
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, err := writableCellar(snapshot, cellarID); err != nil {
			return err
		}
		formula, err := cellarFormula(snapshot, cellarID, id)
		if err != nil {
			return err
		}
		if err := s.checkExpected(formula.Version, input.ExpectedVersion); err != nil {
			return err
		}
		for _, run := range snapshot.Runs {
			if run.CellarID == cellarID && run.FormulaID == id &&
				run.State != domain.RunCompleted && run.State != domain.RunAborted {
				return domain.NewError(domain.CodeFormulaInUse,
					"formula is attached to a run that is still open")
			}
		}
		if err := formula.Retire(s.clock.Now()); err != nil {
			return err
		}
		snapshot.Formulas[id] = formula
		updated = formula.Clone()
		return nil
	})
	if err != nil {
		return domain.Formula{}, err
	}
	return updated, nil
}

func (s *Service) GetFormula(cellarID, id string) (domain.Formula, error) {
	var found domain.Formula
	err := s.store.View(func(snapshot domain.Snapshot) error {
		if _, err := snapshotCellar(&snapshot, cellarID); err != nil {
			return err
		}
		formula, err := cellarFormula(&snapshot, cellarID, id)
		if err != nil {
			return err
		}
		found = formula.Clone()
		return nil
	})
	if err != nil {
		return domain.Formula{}, err
	}
	return found, nil
}

func (s *Service) ListFormulas(cellarID string) ([]domain.Formula, error) {
	var formulas []domain.Formula
	err := s.store.View(func(snapshot domain.Snapshot) error {
		if _, err := snapshotCellar(&snapshot, cellarID); err != nil {
			return err
		}
		formulas = make([]domain.Formula, 0, len(snapshot.Formulas))
		for _, formula := range snapshot.Formulas {
			if formula.CellarID == cellarID {
				formulas = append(formulas, formula.Clone())
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(formulas, func(i, j int) bool {
		if formulas[i].Name == formulas[j].Name {
			return formulas[i].ID < formulas[j].ID
		}
		return formulas[i].Name < formulas[j].Name
	})
	return formulas, nil
}

// cellarFormula resolves a formula strictly inside one cellar; a formula
// from another cellar is reported as not found so namespaces never leak.
func cellarFormula(
	snapshot *domain.Snapshot,
	cellarID string,
	id string,
) (domain.Formula, error) {
	formula, ok := snapshot.Formulas[id]
	if !ok || formula.CellarID != cellarID {
		return domain.Formula{}, domain.NewError(domain.CodeFormulaNotFound,
			"formula was not found")
	}
	return formula, nil
}

func timestampPointer(value time.Time) *time.Time {
	copy := value.UTC()
	return &copy
}
