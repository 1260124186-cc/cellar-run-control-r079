package service

import (
	"strings"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/clock"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/storage"
)

type Service struct {
	store *storage.Store
	clock clock.Clock
}

func New(store *storage.Store, timeSource clock.Clock) *Service {
	if timeSource == nil {
		timeSource = clock.System{}
	}
	return &Service{store: store, clock: timeSource}
}

func (s *Service) Revision() int64 {
	return s.store.Revision()
}

func (s *Service) checkExpected(current int64, expected *int64) error {
	if expected != nil && current != *expected {
		return domain.NewError(domain.CodeRevisionConflict,
			"the aggregate version does not match X-Expected-Version")
	}
	return nil
}

// requireSite resolves the namespace for a read operation. Archived sites
// remain readable so historical runs stay visible in their original space.
func requireSite(snapshot domain.Snapshot, siteID string) (domain.Site, error) {
	site, ok := snapshot.Sites[siteID]
	if !ok {
		return domain.Site{}, domain.NewError(domain.CodeSiteNotFound, "site was not found")
	}
	return site, nil
}

// requireWritableSite resolves the namespace for a write operation. It runs
// inside the same locked transaction as the mutation, so a concurrent site
// archival either wins (and the write fails) or loses (and the write
// commits) without producing partial cross-site state.
func requireWritableSite(snapshot *domain.Snapshot, siteID string) (domain.Site, error) {
	site, err := requireSite(*snapshot, siteID)
	if err != nil {
		return domain.Site{}, err
	}
	if site.State != domain.SiteActive {
		return domain.Site{}, domain.NewError(domain.CodeSiteArchived,
			"site is archived and no longer accepts writes")
	}
	return site, nil
}

func generateEntityID(prefix string) (string, error) {
	return domain.NewID(prefix)
}

func generatedRunCode() (string, error) {
	value, err := domain.NewID("run")
	if err != nil {
		return "", err
	}
	suffix := strings.ToUpper(strings.TrimPrefix(value, "run_"))
	return "RUN-" + suffix, nil
}
