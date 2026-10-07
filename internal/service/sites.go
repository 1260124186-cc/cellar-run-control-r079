package service

import (
	"sort"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func (s *Service) CreateSite(input CreateSiteInput) (domain.Site, error) {
	now := s.clock.Now()
	site := domain.Site{
		Code:      input.Code,
		Name:      input.Name,
		State:     domain.SiteActive,
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	site.Normalize()
	if err := site.Validate(); err != nil {
		return domain.Site{}, err
	}
	id, err := generateEntityID("site")
	if err != nil {
		return domain.Site{}, domain.NewError(domain.CodeStorage, err.Error())
	}
	site.ID = id

	err = s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, exists := snapshot.SiteIndex[site.Code]; exists {
			return domain.NewError(domain.CodeSiteConflict,
				"a site with the same code already exists")
		}
		snapshot.Sites[site.ID] = site
		snapshot.SiteIndex[site.Code] = site.ID
		return nil
	})
	if err != nil {
		return domain.Site{}, err
	}
	return site.Clone(), nil
}

func (s *Service) GetSite(id string) (domain.Site, error) {
	var found domain.Site
	err := s.store.View(func(snapshot domain.Snapshot) error {
		site, ok := snapshot.Sites[id]
		if !ok {
			return domain.NewError(domain.CodeSiteNotFound, "site was not found")
		}
		found = site.Clone()
		return nil
	})
	if err != nil {
		return domain.Site{}, err
	}
	return found, nil
}

func (s *Service) ListSites() ([]domain.Site, error) {
	var sites []domain.Site
	err := s.store.View(func(snapshot domain.Snapshot) error {
		sites = make([]domain.Site, 0, len(snapshot.Sites))
		for _, site := range snapshot.Sites {
			sites = append(sites, site.Clone())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].Code == sites[j].Code {
			return sites[i].ID < sites[j].ID
		}
		return sites[i].Code < sites[j].Code
	})
	return sites, nil
}

func (s *Service) ArchiveSite(id string, input VersionInput) (domain.Site, error) {
	var updated domain.Site
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		site, ok := snapshot.Sites[id]
		if !ok {
			return domain.NewError(domain.CodeSiteNotFound, "site was not found")
		}
		if err := s.checkExpected(site.Version, input.ExpectedVersion); err != nil {
			return err
		}
		if err := site.Archive(s.clock.Now()); err != nil {
			return err
		}
		snapshot.Sites[id] = site
		updated = site.Clone()
		return nil
	})
	if err != nil {
		return domain.Site{}, err
	}
	return updated, nil
}
