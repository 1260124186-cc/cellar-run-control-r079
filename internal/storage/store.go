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
	normalized, err := normalizeSnapshot(loaded, s.now())
	if err != nil {
		return err
	}
	s.snapshot = normalized
	return nil
}

func normalizeSnapshot(value domain.Snapshot, now time.Time) (domain.Snapshot, error) {
	if value.Revision < 0 {
		return domain.Snapshot{}, fmt.Errorf("state revision cannot be negative")
	}
	if value.Sites == nil {
		value.Sites = map[string]domain.Site{}
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
	if value.SiteIndex == nil {
		value.SiteIndex = map[string]string{}
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
	if needsSiteMigration(value) {
		value = migrateLegacySnapshot(value, now)
	}
	if err := validateIndexes(value); err != nil {
		return domain.Snapshot{}, err
	}
	return value, nil
}

// needsSiteMigration reports whether the snapshot predates cellar namespaces:
// it holds entities but no site records at all.
func needsSiteMigration(value domain.Snapshot) bool {
	if len(value.Sites) > 0 {
		return false
	}
	return len(value.Formulas) > 0 || len(value.Vessels) > 0 || len(value.Runs) > 0
}

// legacySiteID is the stable identifier of the default site that receives
// entities stored before cellar namespaces existed.
const legacySiteID = "site_legacy_main"

// migrateLegacySnapshot moves every pre-namespace entity into one default
// site and rebuilds the uniqueness indexes with site-scoped keys. Legacy
// snapshots enforced global uniqueness, so the rebuilt keys cannot collide.
func migrateLegacySnapshot(value domain.Snapshot, now time.Time) domain.Snapshot {
	stamp := now.UTC()
	if !value.UpdatedAt.IsZero() {
		stamp = value.UpdatedAt.UTC()
	}
	site := domain.Site{
		ID:        legacySiteID,
		Code:      "MAIN",
		Name:      "Main Cellar",
		State:     domain.SiteActive,
		Version:   1,
		CreatedAt: stamp,
		UpdatedAt: stamp,
	}
	value.Sites[site.ID] = site
	value.SiteIndex = map[string]string{site.Code: site.ID}

	for id, formula := range value.Formulas {
		formula.SiteID = site.ID
		value.Formulas[id] = formula
	}
	for id, vessel := range value.Vessels {
		vessel.SiteID = site.ID
		value.Vessels[id] = vessel
	}
	for id, run := range value.Runs {
		run.SiteID = site.ID
		value.Runs[id] = run
	}

	value.NameIndex = make(map[string]string, len(value.Formulas))
	for id, formula := range value.Formulas {
		value.NameIndex[domain.ScopedIndexKey(site.ID, domain.NameKey(formula.Name))] = id
	}
	value.VesselIndex = make(map[string]string, len(value.Vessels))
	for id, vessel := range value.Vessels {
		value.VesselIndex[domain.ScopedIndexKey(site.ID, vessel.Code)] = id
	}
	value.RunIndex = make(map[string]string, len(value.Runs))
	for id, run := range value.Runs {
		value.RunIndex[domain.ScopedIndexKey(site.ID, run.Code)] = id
	}
	return value
}

func validateIndexes(value domain.Snapshot) error {
	expectedSites := make(map[string]string, len(value.Sites))
	for id, site := range value.Sites {
		if existing, found := expectedSites[site.Code]; found && existing != id {
			return fmt.Errorf("site index contains a duplicate code")
		}
		expectedSites[site.Code] = id
	}
	if err := compareIndexes("site code", value.SiteIndex, expectedSites); err != nil {
		return err
	}

	expectedNames := make(map[string]string, len(value.Formulas))
	for id, formula := range value.Formulas {
		if _, ok := value.Sites[formula.SiteID]; !ok {
			return fmt.Errorf("formula %s references an unknown site", id)
		}
		key := domain.NameKey(formula.Name)
		if key == "" {
			return fmt.Errorf("formula %s has an empty normalized name", id)
		}
		scoped := domain.ScopedIndexKey(formula.SiteID, key)
		if existing, found := expectedNames[scoped]; found && existing != id {
			return fmt.Errorf("formula name index contains a duplicate key")
		}
		expectedNames[scoped] = id
	}
	if err := compareIndexes("formula name", value.NameIndex, expectedNames); err != nil {
		return err
	}

	expectedVessels := make(map[string]string, len(value.Vessels))
	for id, vessel := range value.Vessels {
		if _, ok := value.Sites[vessel.SiteID]; !ok {
			return fmt.Errorf("vessel %s references an unknown site", id)
		}
		scoped := domain.ScopedIndexKey(vessel.SiteID, vessel.Code)
		if existing, found := expectedVessels[scoped]; found && existing != id {
			return fmt.Errorf("vessel index contains a duplicate code")
		}
		expectedVessels[scoped] = id
		if vessel.ActiveRunID != nil {
			run, ok := value.Runs[*vessel.ActiveRunID]
			if !ok || run.SiteID != vessel.SiteID {
				return fmt.Errorf("vessel %s references a run outside its site", id)
			}
		}
	}
	if err := compareIndexes("vessel code", value.VesselIndex, expectedVessels); err != nil {
		return err
	}

	expectedRuns := make(map[string]string, len(value.Runs))
	for id, run := range value.Runs {
		if _, ok := value.Sites[run.SiteID]; !ok {
			return fmt.Errorf("run %s references an unknown site", id)
		}
		scoped := domain.ScopedIndexKey(run.SiteID, run.Code)
		if existing, found := expectedRuns[scoped]; found && existing != id {
			return fmt.Errorf("run index contains a duplicate code")
		}
		expectedRuns[scoped] = id
		formula, ok := value.Formulas[run.FormulaID]
		if !ok || formula.SiteID != run.SiteID {
			return fmt.Errorf("run %s references a formula outside its site", id)
		}
		if run.VesselID != nil {
			vessel, ok := value.Vessels[*run.VesselID]
			if !ok || vessel.SiteID != run.SiteID {
				return fmt.Errorf("run %s references a vessel outside its site", id)
			}
		}
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
