package service

import (
	"sort"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func (s *Service) CreateRun(
	cellarID string,
	input CreateRunInput,
) (domain.FermentationRun, error) {
	now := s.clock.Now()
	code := domain.NormalizeRunCode(input.Code)
	if code == "" {
		generated, err := generatedRunCode()
		if err != nil {
			return domain.FermentationRun{}, domain.NewError(domain.CodeStorage, err.Error())
		}
		code = generated
	}
	run := domain.FermentationRun{
		CellarID:       cellarID,
		Code:           code,
		FormulaID:      input.FormulaID,
		PlannedVolumeL: input.PlannedVolumeL,
		Notes:          input.Notes,
		State:          domain.RunPlanned,
		Observations:   []domain.GravityObservation{},
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := run.Validate(); err != nil {
		return domain.FermentationRun{}, err
	}
	id, err := generateEntityID("run")
	if err != nil {
		return domain.FermentationRun{}, domain.NewError(domain.CodeStorage, err.Error())
	}
	run.ID = id
	codeKey := domain.ScopedIndexKey(cellarID, run.Code)

	err = s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, err := writableCellar(snapshot, cellarID); err != nil {
			return err
		}
		formula, err := cellarFormula(snapshot, cellarID, run.FormulaID)
		if err != nil {
			return err
		}
		if formula.State != domain.FormulaApproved {
			return domain.NewError(domain.CodeFormulaState,
				"run requires an approved formula")
		}
		if _, exists := snapshot.RunIndex[codeKey]; exists {
			return domain.NewError(domain.CodeRunConflict,
				"a run with the same code already exists in this cellar")
		}
		snapshot.Runs[run.ID] = run.Clone()
		snapshot.RunIndex[codeKey] = run.ID
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, err
	}
	return run.Clone(), nil
}

func (s *Service) ReserveRun(
	cellarID string,
	id string,
	input ReserveRunInput,
) (domain.FermentationRun, domain.Vessel, error) {
	var updatedRun domain.FermentationRun
	var updatedVessel domain.Vessel
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, err := writableCellar(snapshot, cellarID); err != nil {
			return err
		}
		run, err := cellarRun(snapshot, cellarID, id)
		if err != nil {
			return err
		}
		if err := s.checkExpected(run.Version, input.ExpectedVersion); err != nil {
			return err
		}
		vessel, err := cellarVessel(snapshot, cellarID, input.VesselID)
		if err != nil {
			return err
		}
		if run.PlannedVolumeL > vessel.CapacityL {
			return domain.FieldError(domain.CodeInvalidInput,
				"planned_volume_liters", "exceeds vessel capacity")
		}
		if err := vessel.Reserve(run.ID, s.clock.Now()); err != nil {
			return err
		}
		if err := run.ReserveVessel(vessel.ID, s.clock.Now()); err != nil {
			return err
		}
		snapshot.Runs[id] = run.Clone()
		snapshot.Vessels[vessel.ID] = vessel
		updatedRun = run.Clone()
		updatedVessel = vessel
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, domain.Vessel{}, err
	}
	return updatedRun, updatedVessel, nil
}

func (s *Service) StartRun(
	cellarID string,
	id string,
	input VersionInput,
) (domain.FermentationRun, domain.Vessel, error) {
	var updatedRun domain.FermentationRun
	var updatedVessel domain.Vessel
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, err := writableCellar(snapshot, cellarID); err != nil {
			return err
		}
		run, err := cellarRun(snapshot, cellarID, id)
		if err != nil {
			return err
		}
		if err := s.checkExpected(run.Version, input.ExpectedVersion); err != nil {
			return err
		}
		if run.VesselID == nil {
			return domain.NewError(domain.CodeRunState, "run has no vessel")
		}
		vessel, err := cellarVessel(snapshot, cellarID, *run.VesselID)
		if err != nil {
			return err
		}
		if err := vessel.StartRun(run.ID, s.clock.Now()); err != nil {
			return err
		}
		if err := run.StartFermentation(s.clock.Now()); err != nil {
			return err
		}
		snapshot.Runs[id] = run.Clone()
		snapshot.Vessels[vessel.ID] = vessel
		updatedRun = run.Clone()
		updatedVessel = vessel
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, domain.Vessel{}, err
	}
	return updatedRun, updatedVessel, nil
}

func (s *Service) AbortRun(
	cellarID string,
	id string,
	input VersionInput,
) (domain.FermentationRun, domain.Vessel, error) {
	var updatedRun domain.FermentationRun
	var updatedVessel domain.Vessel
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, err := writableCellar(snapshot, cellarID); err != nil {
			return err
		}
		run, err := cellarRun(snapshot, cellarID, id)
		if err != nil {
			return err
		}
		if err := s.checkExpected(run.Version, input.ExpectedVersion); err != nil {
			return err
		}
		hadStarted := run.HadStarted()
		if err := run.Abort(s.clock.Now()); err != nil {
			return err
		}
		if run.VesselID != nil {
			vessel, err := cellarVessel(snapshot, cellarID, *run.VesselID)
			if err != nil {
				return err
			}
			if hadStarted {
				if err := vessel.StartCleaning(run.ID, s.clock.Now()); err != nil {
					return err
				}
			} else if err := vessel.ReleaseReservation(run.ID, s.clock.Now()); err != nil {
				return err
			}
			snapshot.Vessels[vessel.ID] = vessel
			updatedVessel = vessel
		}
		snapshot.Runs[id] = run.Clone()
		updatedRun = run.Clone()
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, domain.Vessel{}, err
	}
	return updatedRun, updatedVessel, nil
}

func (s *Service) GetRun(cellarID, id string) (domain.FermentationRun, error) {
	var found domain.FermentationRun
	err := s.store.View(func(snapshot domain.Snapshot) error {
		if _, err := snapshotCellar(&snapshot, cellarID); err != nil {
			return err
		}
		run, err := cellarRun(&snapshot, cellarID, id)
		if err != nil {
			return err
		}
		found = run.Clone()
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, err
	}
	return found, nil
}

func (s *Service) ListRuns(cellarID string) ([]domain.FermentationRun, error) {
	var runs []domain.FermentationRun
	err := s.store.View(func(snapshot domain.Snapshot) error {
		if _, err := snapshotCellar(&snapshot, cellarID); err != nil {
			return err
		}
		runs = make([]domain.FermentationRun, 0, len(snapshot.Runs))
		for _, run := range snapshot.Runs {
			if run.CellarID == cellarID {
				runs = append(runs, run.Clone())
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].Code == runs[j].Code {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].Code < runs[j].Code
	})
	return runs, nil
}

// cellarRun resolves a run strictly inside one cellar; a run from another
// cellar is reported as not found so namespaces never leak.
func cellarRun(
	snapshot *domain.Snapshot,
	cellarID string,
	id string,
) (domain.FermentationRun, error) {
	run, ok := snapshot.Runs[id]
	if !ok || run.CellarID != cellarID {
		return domain.FermentationRun{}, domain.NewError(domain.CodeRunNotFound,
			"run was not found")
	}
	return run, nil
}
