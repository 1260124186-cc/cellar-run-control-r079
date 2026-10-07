package service

import (
	"sort"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func (s *Service) CreateVessel(
	cellarID string,
	input CreateVesselInput,
) (domain.Vessel, error) {
	now := s.clock.Now()
	vessel := domain.Vessel{
		CellarID:  cellarID,
		Code:      domain.NormalizeVesselCode(input.Code),
		CapacityL: input.CapacityL,
		State:     domain.VesselAvailable,
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := vessel.Validate(); err != nil {
		return domain.Vessel{}, err
	}
	id, err := generateEntityID("vessel")
	if err != nil {
		return domain.Vessel{}, domain.NewError(domain.CodeStorage, err.Error())
	}
	vessel.ID = id
	codeKey := domain.ScopedIndexKey(cellarID, vessel.Code)

	err = s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, err := writableCellar(snapshot, cellarID); err != nil {
			return err
		}
		if _, exists := snapshot.VesselIndex[codeKey]; exists {
			return domain.NewError(domain.CodeVesselConflict,
				"a vessel with the same code already exists in this cellar")
		}
		snapshot.Vessels[vessel.ID] = vessel
		snapshot.VesselIndex[codeKey] = vessel.ID
		return nil
	})
	if err != nil {
		return domain.Vessel{}, err
	}
	return vessel, nil
}

func (s *Service) GetVessel(cellarID, id string) (domain.Vessel, error) {
	var found domain.Vessel
	err := s.store.View(func(snapshot domain.Snapshot) error {
		if _, err := snapshotCellar(&snapshot, cellarID); err != nil {
			return err
		}
		vessel, err := cellarVessel(&snapshot, cellarID, id)
		if err != nil {
			return err
		}
		found = vessel
		return nil
	})
	if err != nil {
		return domain.Vessel{}, err
	}
	return found, nil
}

func (s *Service) ListVessels(cellarID string) ([]domain.Vessel, error) {
	var vessels []domain.Vessel
	err := s.store.View(func(snapshot domain.Snapshot) error {
		if _, err := snapshotCellar(&snapshot, cellarID); err != nil {
			return err
		}
		vessels = make([]domain.Vessel, 0, len(snapshot.Vessels))
		for _, vessel := range snapshot.Vessels {
			if vessel.CellarID == cellarID {
				vessels = append(vessels, vessel)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(vessels, func(i, j int) bool {
		if vessels[i].Code == vessels[j].Code {
			return vessels[i].ID < vessels[j].ID
		}
		return vessels[i].Code < vessels[j].Code
	})
	return vessels, nil
}

func (s *Service) FinishCleaning(
	cellarID string,
	id string,
	input FinishCleaningInput,
) (domain.Vessel, error) {
	var updated domain.Vessel
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, err := writableCellar(snapshot, cellarID); err != nil {
			return err
		}
		vessel, err := cellarVessel(snapshot, cellarID, id)
		if err != nil {
			return err
		}
		if err := s.checkExpected(vessel.Version, input.ExpectedVersion); err != nil {
			return err
		}
		if vessel.ActiveRunID == nil {
			return domain.NewError(domain.CodeVesselState,
				"cleaning vessel has no associated run")
		}
		run, ok := snapshot.Runs[*vessel.ActiveRunID]
		if !ok || run.CellarID != cellarID {
			return domain.NewError(domain.CodeRunNotFound,
				"associated run was not found")
		}
		if run.State != domain.RunCompleted && run.State != domain.RunAborted {
			return domain.NewError(domain.CodeRunState,
				"associated run is still open")
		}
		if err := vessel.FinishCleaning(s.clock.Now()); err != nil {
			return err
		}
		snapshot.Vessels[id] = vessel
		updated = vessel
		return nil
	})
	if err != nil {
		return domain.Vessel{}, err
	}
	return updated, nil
}

func (s *Service) RetireVessel(
	cellarID string,
	id string,
	input VersionInput,
) (domain.Vessel, error) {
	var updated domain.Vessel
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, err := writableCellar(snapshot, cellarID); err != nil {
			return err
		}
		vessel, err := cellarVessel(snapshot, cellarID, id)
		if err != nil {
			return err
		}
		if err := s.checkExpected(vessel.Version, input.ExpectedVersion); err != nil {
			return err
		}
		if err := vessel.Retire(s.clock.Now()); err != nil {
			return err
		}
		snapshot.Vessels[id] = vessel
		updated = vessel
		return nil
	})
	if err != nil {
		return domain.Vessel{}, err
	}
	return updated, nil
}

// cellarVessel resolves a vessel strictly inside one cellar; a vessel from
// another cellar is reported as not found so namespaces never leak.
func cellarVessel(
	snapshot *domain.Snapshot,
	cellarID string,
	id string,
) (domain.Vessel, error) {
	vessel, ok := snapshot.Vessels[id]
	if !ok || vessel.CellarID != cellarID {
		return domain.Vessel{}, domain.NewError(domain.CodeVesselNotFound,
			"vessel was not found")
	}
	return vessel, nil
}
