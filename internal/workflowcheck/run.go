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

// harness owns the temporary snapshot directory and the running check server
// so a workflow check can restart the service against the same data.
type harness struct {
	dataDir string
	server  *httptest.Server
	api     *client
}

func newHarness() (*harness, error) {
	dataDir, err := os.MkdirTemp("", "cellar-run-check-*")
	if err != nil {
		return nil, fmt.Errorf("create check directory: %w", err)
	}
	h := &harness{dataDir: dataDir}
	if err := h.restart(); err != nil {
		_ = os.RemoveAll(dataDir)
		return nil, err
	}
	return h, nil
}

// restart closes the current server (if any) and reopens the snapshot from
// disk, simulating a full service restart against the same data directory.
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
	_ = os.RemoveAll(h.dataDir)
}

func Run(name string) error {
	h, err := newHarness()
	if err != nil {
		return err
	}
	defer h.close()

	switch name {
	case "formula-approval":
		err = formulaApproval(h.api)
	case "vessel-run-lifecycle":
		err = vesselRunLifecycle(h.api)
	case "observations-completion":
		err = observationsCompletion(h.api)
	case "site-isolation":
		err = siteIsolation(h)
	default:
		return fmt.Errorf("unknown workflow check %q", name)
	}
	if err != nil {
		return err
	}
	fmt.Printf("workflow check passed: %s\n", name)
	return nil
}
