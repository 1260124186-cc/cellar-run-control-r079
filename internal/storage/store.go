package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

const stateFileName = "state.json"

type Store struct {
	mu       sync.RWMutex
	path     string
	snapshot domain.Snapshot
	now      func() time.Time
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	store := &Store{
		path:     filepath.Join(dataDir, stateFileName),
		snapshot: domain.EmptySnapshot(),
		now:      func() time.Time { return time.Now().UTC() },
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	var loaded domain.Snapshot
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return fmt.Errorf("decode state: %w", err)
	}
	normalized, err := normalizeSnapshot(loaded)
	if err != nil {
		return err
	}
	s.snapshot = normalized
	return nil
}

func normalizeSnapshot(value domain.Snapshot) (domain.Snapshot, error) {
	if value.Revision < 0 {
		return domain.Snapshot{}, fmt.Errorf("state revision cannot be negative")
	}
	if value.Cellars == nil {
		value.Cellars = map[string]domain.Cellar{}
	}
	if value.Formulas == nil {
		value.Formulas = map[string]domain.Formula{}
	}
	if value.Vessels == nil {
		value.Vessels = map[string]domain.Vessel{}
	}
	if value.Runs == nil {
		value.Runs = map[string]domain.FermentationRun{}
	}
	if value.CellarIndex == nil {
		value.CellarIndex = map[string]string{}
	}
	if value.NameIndex == nil {
		value.NameIndex = map[string]string{}
	}
	if value.VesselIndex == nil {
		value.VesselIndex = map[string]string{}
	}
	if value.RunIndex == nil {
		value.RunIndex = map[string]string{}
	}
	migrated := migrateLegacyEntities(&value)
	if err := validateCellarReferences(value); err != nil {
		return domain.Snapshot{}, err
	}
	if migrated {
		// A pre-namespace snapshot carried unscoped indexes; rebuild the
		// scoped indexes deterministically from the migrated entities.
		rebuildIndexes(&value)
		return value, nil
	}
	if err := validateIndexes(value); err != nil {
		return domain.Snapshot{}, err
	}
	return value, nil
}

// migrateLegacyEntities moves entities stored before cellar namespaces
// existed into a deterministic legacy cellar so historical data stays
// reachable after the upgrade. It reports whether any entity was migrated.
func migrateLegacyEntities(value *domain.Snapshot) bool {
	needs := false
	for _, formula := range value.Formulas {
		if formula.CellarID == "" {
			needs = true
			break
		}
	}
	if !needs {
		for _, vessel := range value.Vessels {
			if vessel.CellarID == "" {
				needs = true
				break
			}
		}
	}
	if !needs {
		for _, run := range value.Runs {
			if run.CellarID == "" {
				needs = true
				break
			}
		}
	}
	if !needs {
		return false
	}
	if _, ok := value.Cellars[domain.LegacyCellarID]; !ok {
		stamp := value.UpdatedAt
		if stamp.IsZero() {
			stamp = time.Now().UTC()
		}
		value.Cellars[domain.LegacyCellarID] = domain.Cellar{
			ID:        domain.LegacyCellarID,
			Name:      "Legacy Cellar",
			State:     domain.CellarActive,
			Version:   1,
			CreatedAt: stamp,
			UpdatedAt: stamp,
		}
	}
	for id, formula := range value.Formulas {
		if formula.CellarID == "" {
			formula.CellarID = domain.LegacyCellarID
			value.Formulas[id] = formula
		}
	}
	for id, vessel := range value.Vessels {
		if vessel.CellarID == "" {
			vessel.CellarID = domain.LegacyCellarID
			value.Vessels[id] = vessel
		}
	}
	for id, run := range value.Runs {
		if run.CellarID == "" {
			run.CellarID = domain.LegacyCellarID
			value.Runs[id] = run
		}
	}
	return true
}

// validateCellarReferences rejects snapshots whose entities point outside
// their own cellar, so a corrupt or hand-edited file stops startup instead
// of silently mixing namespaces.
func validateCellarReferences(value domain.Snapshot) error {
	for id, formula := range value.Formulas {
		if _, ok := value.Cellars[formula.CellarID]; !ok {
			return fmt.Errorf("formula %s references an unknown cellar", id)
		}
	}
	for id, vessel := range value.Vessels {
		if _, ok := value.Cellars[vessel.CellarID]; !ok {
			return fmt.Errorf("vessel %s references an unknown cellar", id)
		}
		if vessel.ActiveRunID != nil {
			run, ok := value.Runs[*vessel.ActiveRunID]
			if !ok {
				return fmt.Errorf("vessel %s references an unknown run", id)
			}
			if run.CellarID != vessel.CellarID {
				return fmt.Errorf("vessel %s references a run in another cellar", id)
			}
		}
	}
	for id, run := range value.Runs {
		if _, ok := value.Cellars[run.CellarID]; !ok {
			return fmt.Errorf("run %s references an unknown cellar", id)
		}
		formula, ok := value.Formulas[run.FormulaID]
		if !ok {
			return fmt.Errorf("run %s references an unknown formula", id)
		}
		if formula.CellarID != run.CellarID {
			return fmt.Errorf("run %s references a formula in another cellar", id)
		}
		if run.VesselID != nil {
			vessel, ok := value.Vessels[*run.VesselID]
			if !ok {
				return fmt.Errorf("run %s references an unknown vessel", id)
			}
			if vessel.CellarID != run.CellarID {
				return fmt.Errorf("run %s references a vessel in another cellar", id)
			}
		}
	}
	return nil
}

func rebuildIndexes(value *domain.Snapshot) {
	value.CellarIndex = make(map[string]string, len(value.Cellars))
	for id, cellar := range value.Cellars {
		value.CellarIndex[domain.NameKey(cellar.Name)] = id
	}
	value.NameIndex = make(map[string]string, len(value.Formulas))
	for id, formula := range value.Formulas {
		value.NameIndex[domain.ScopedIndexKey(formula.CellarID, domain.NameKey(formula.Name))] = id
	}
	value.VesselIndex = make(map[string]string, len(value.Vessels))
	for id, vessel := range value.Vessels {
		value.VesselIndex[domain.ScopedIndexKey(vessel.CellarID, vessel.Code)] = id
	}
	value.RunIndex = make(map[string]string, len(value.Runs))
	for id, run := range value.Runs {
		value.RunIndex[domain.ScopedIndexKey(run.CellarID, run.Code)] = id
	}
}

func validateIndexes(value domain.Snapshot) error {
	expectedCellars := make(map[string]string, len(value.Cellars))
	for id, cellar := range value.Cellars {
		key := domain.NameKey(cellar.Name)
		if key == "" {
			return fmt.Errorf("cellar %s has an empty normalized name", id)
		}
		if existing, found := expectedCellars[key]; found && existing != id {
			return fmt.Errorf("cellar name index contains a duplicate key")
		}
		expectedCellars[key] = id
	}
	if err := compareIndexes("cellar name", value.CellarIndex, expectedCellars); err != nil {
		return err
	}

	expectedNames := make(map[string]string, len(value.Formulas))
	for id, formula := range value.Formulas {
		nameKey := domain.NameKey(formula.Name)
		if nameKey == "" {
			return fmt.Errorf("formula %s has an empty normalized name", id)
		}
		key := domain.ScopedIndexKey(formula.CellarID, nameKey)
		if existing, found := expectedNames[key]; found && existing != id {
			return fmt.Errorf("formula name index contains a duplicate key")
		}
		expectedNames[key] = id
	}
	if err := compareIndexes("formula name", value.NameIndex, expectedNames); err != nil {
		return err
	}

	expectedVessels := make(map[string]string, len(value.Vessels))
	for id, vessel := range value.Vessels {
		key := domain.ScopedIndexKey(vessel.CellarID, vessel.Code)
		if existing, found := expectedVessels[key]; found && existing != id {
			return fmt.Errorf("vessel index contains a duplicate code")
		}
		expectedVessels[key] = id
	}
	if err := compareIndexes("vessel code", value.VesselIndex, expectedVessels); err != nil {
		return err
	}

	expectedRuns := make(map[string]string, len(value.Runs))
	for id, run := range value.Runs {
		key := domain.ScopedIndexKey(run.CellarID, run.Code)
		if existing, found := expectedRuns[key]; found && existing != id {
			return fmt.Errorf("run index contains a duplicate code")
		}
		expectedRuns[key] = id
	}
	return compareIndexes("run code", value.RunIndex, expectedRuns)
}

func compareIndexes(label string, actual, expected map[string]string) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("%s index does not match its collection", label)
	}
	for key, expectedID := range expected {
		actualID, found := actual[key]
		if !found || actualID != expectedID {
			return fmt.Errorf("%s index does not match its collection", label)
		}
	}
	return nil
}

func (s *Store) Revision() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot.Revision
}

func (s *Store) View(visitor func(domain.Snapshot) error) error {
	s.mu.RLock()
	copied := s.snapshot.Clone()
	s.mu.RUnlock()
	return visitor(copied)
}

func (s *Store) Update(expected *int64, mutate func(*domain.Snapshot) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if expected != nil && *expected != s.snapshot.Revision {
		return domain.NewError(domain.CodeRevisionConflict,
			"the supplied revision does not match the current snapshot")
	}
	candidate := s.snapshot.Clone()
	if err := mutate(&candidate); err != nil {
		return err
	}
	candidate.Revision = s.snapshot.Revision + 1
	candidate.UpdatedAt = s.now()
	if err := writeSnapshot(s.path, candidate); err != nil {
		return domain.NewError(domain.CodeStorage, err.Error())
	}
	s.snapshot = candidate
	return nil
}

func (s *Store) Execute(mutator func(*domain.Snapshot) error) error {
	return s.Update(nil, mutator)
}
