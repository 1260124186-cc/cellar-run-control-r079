package workflowcheck

import (
	"fmt"
	"net/http/httptest"
	"os"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/clock"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/httpapi"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/service"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/storage"
)

// harness owns the check server and its data directory so a check can
// restart the service against the same snapshot and verify persistence.
type harness struct {
	dataDir string
	server  *httptest.Server
	api     *client
}

func newHarness(dataDir string) (*harness, error) {
	h := &harness{dataDir: dataDir}
	if err := h.restart(); err != nil {
		return nil, err
	}
	return h, nil
}

// restart closes the current server and reopens the store from the same
// directory, simulating a full service restart over persisted state.
func (h *harness) restart() error {
	if h.server != nil {
		h.server.Close()
	}
	store, err := storage.Open(h.dataDir)
	if err != nil {
		return err
	}
	app := service.New(store, clock.System{})
	h.server = httptest.NewServer(httpapi.NewHandler(app))
	h.api = newClient(h.server.URL)
	return nil
}

func (h *harness) close() {
	if h.server != nil {
		h.server.Close()
	}
}

func Run(name string) error {
	dataDir, err := os.MkdirTemp("", "cellar-run-check-*")
	if err != nil {
		return fmt.Errorf("create check directory: %w", err)
	}
	defer os.RemoveAll(dataDir)

	h, err := newHarness(dataDir)
	if err != nil {
		return err
	}
	defer h.close()

	switch name {
	case "formula-approval":
		if err := formulaApproval(h.api); err != nil {
			return err
		}
	case "vessel-run-lifecycle":
		if err := vesselRunLifecycle(h.api); err != nil {
			return err
		}
	case "observations-completion":
		if err := observationsCompletion(h.api); err != nil {
			return err
		}
	case "cellar-namespaces":
		if err := cellarNamespaces(h); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown workflow check %q", name)
	}
	fmt.Printf("workflow check passed: %s\n", name)
	return nil
}
