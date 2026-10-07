package domain

import "time"

type CellarState string

const (
	CellarActive   CellarState = "active"
	CellarArchived CellarState = "archived"
)

// LegacyCellarID is the deterministic cellar that receives entities stored
// before cellar namespaces existed, so historical data stays reachable.
const LegacyCellarID = "cellar_legacy"

// Cellar is an isolated namespace for one production site. Formulas,
// vessels, and runs belong to exactly one cellar; codes and names are
// unique within a cellar and may repeat across cellars.
type Cellar struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	State      CellarState `json:"state"`
	Version    int64       `json:"version"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
	ArchivedAt *time.Time  `json:"archived_at,omitempty"`
}

func (c *Cellar) Normalize() {
	c.Name = NormalizeName(c.Name)
}

func (c Cellar) Validate() error {
	if c.Name == "" {
		return FieldError(CodeInvalidInput, "name", "is required")
	}
	if len([]rune(c.Name)) > 120 {
		return FieldError(CodeInvalidInput, "name", "must not exceed 120 characters")
	}
	return nil
}

func (c *Cellar) Archive(now time.Time) error {
	if c.State != CellarActive {
		return NewError(CodeCellarState, "only an active cellar can be archived")
	}
	stamp := now.UTC()
	c.State = CellarArchived
	c.ArchivedAt = &stamp
	c.UpdatedAt = stamp
	c.Version++
	return nil
}
