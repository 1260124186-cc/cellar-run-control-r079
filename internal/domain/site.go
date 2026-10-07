package domain

import "time"

type SiteState string

const (
	SiteActive   SiteState = "active"
	SiteArchived SiteState = "archived"
)

// Site is a cellar namespace. Formulas, vessels, and runs belong to exactly
// one site, and their names and codes are unique only within that site.
type Site struct {
	ID         string     `json:"id"`
	Code       string     `json:"code"`
	Name       string     `json:"name"`
	State      SiteState  `json:"state"`
	Version    int64      `json:"version"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
}

func (s Site) Clone() Site {
	cloned := s
	if s.ArchivedAt != nil {
		value := *s.ArchivedAt
		cloned.ArchivedAt = &value
	}
	return cloned
}

func (s *Site) Normalize() {
	s.Code = NormalizeSiteCode(s.Code)
	s.Name = NormalizeName(s.Name)
}

func (s Site) Validate() error {
	if err := ValidateSiteCode(s.Code, "code"); err != nil {
		return err
	}
	if s.Name == "" {
		return FieldError(CodeInvalidInput, "name", "is required")
	}
	if len([]rune(s.Name)) > 120 {
		return FieldError(CodeInvalidInput, "name", "must not exceed 120 characters")
	}
	return nil
}

func (s *Site) Archive(now time.Time) error {
	if s.State != SiteActive {
		return NewError(CodeSiteState, "only an active site can be archived")
	}
	stamp := now.UTC()
	s.State = SiteArchived
	s.ArchivedAt = &stamp
	s.UpdatedAt = stamp
	s.Version++
	return nil
}
