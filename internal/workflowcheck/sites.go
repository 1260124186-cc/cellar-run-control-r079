package workflowcheck

import (
	"fmt"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

// siteIsolation proves that two cellar namespaces share one service without
// leaking: identical names and codes stay unique per site, reads and writes
// cannot cross a site boundary, archiving blocks new writes atomically, and
// a restart leaves every historical run in its original space.
func siteIsolation(h *harness) error {
	api := h.api
	mainSite, err := ensureSite(api, "MAIN", "Main Cellar")
	if err != nil {
		return err
	}
	eastSite, err := ensureSite(api, "EAST", "East Cellar")
	if err != nil {
		return err
	}

	duplicateSite, err := api.call("POST", "/v1/sites", map[string]any{
		"code": " main ",
		"name": "Duplicate Main",
	}, nil)
	if err != nil {
		return err
	}
	if err := requireCode(duplicateSite, 409, string(domain.CodeSiteConflict),
		"create duplicate site code"); err != nil {
		return err
	}

	mainAPI := api.scoped("/v1/sites/" + mainSite.ID)
	eastAPI := api.scoped("/v1/sites/" + eastSite.ID)

	// The same normalized formula name is unique per site, not globally.
	mainFormula, err := ensureApprovedFormula(mainAPI, "Shared Pale")
	if err != nil {
		return err
	}
	eastFormula, err := ensureApprovedFormula(eastAPI, "  shared   pale ")
	if err != nil {
		return err
	}
	if mainFormula.ID == eastFormula.ID {
		return errInvalidWorkflow("sites returned the same formula identifier")
	}
	if mainFormula.SiteID != mainSite.ID || eastFormula.SiteID != eastSite.ID {
		return errInvalidWorkflow("formula was not stamped with its owning site")
	}

	if err := requireFormulaList(mainAPI, mainSite.ID, mainFormula.ID); err != nil {
		return err
	}
	if err := requireFormulaList(eastAPI, eastSite.ID, eastFormula.ID); err != nil {
		return err
	}

	// A detail read through the other site must not find the formula.
	crossFormula, err := eastAPI.call("GET", "/formulas/"+mainFormula.ID, nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(crossFormula, 404, string(domain.CodeFormulaNotFound),
		"read formula across sites"); err != nil {
		return err
	}

	// Identical vessel codes coexist in different sites.
	mainVessel, err := createCheckVessel(mainAPI, "VSL-900", 300)
	if err != nil {
		return err
	}
	eastVessel, err := createCheckVessel(eastAPI, "VSL-900", 300)
	if err != nil {
		return err
	}
	if mainVessel.ID == eastVessel.ID {
		return errInvalidWorkflow("sites returned the same vessel identifier")
	}

	// A run cannot reference another site's formula.
	crossRun, err := mainAPI.call("POST", "/runs",
		createRunPayload("RUN-XREF", eastFormula.ID), nil)
	if err != nil {
		return err
	}
	if err := requireCode(crossRun, 404, string(domain.CodeFormulaNotFound),
		"create run with foreign formula"); err != nil {
		return err
	}

	// Identical run codes coexist in different sites.
	mainRun, err := createPlannedRun(mainAPI, "RUN-SHARED", mainFormula.ID)
	if err != nil {
		return err
	}
	eastRun, err := createPlannedRun(eastAPI, "RUN-SHARED", eastFormula.ID)
	if err != nil {
		return err
	}
	if mainRun.ID == eastRun.ID {
		return errInvalidWorkflow("sites returned the same run identifier")
	}
	if mainRun.SiteID != mainSite.ID || eastRun.SiteID != eastSite.ID {
		return errInvalidWorkflow("run was not stamped with its owning site")
	}

	// A run cannot reserve another site's vessel.
	crossReserve, err := mainAPI.call("POST", "/runs/"+mainRun.ID+"/reserve",
		map[string]any{"vessel_id": eastVessel.ID}, ptrInt64(mainRun.Version))
	if err != nil {
		return err
	}
	if err := requireCode(crossReserve, 404, string(domain.CodeVesselNotFound),
		"reserve vessel across sites"); err != nil {
		return err
	}

	// The same-site lifecycle still works after the rejected attempts.
	if _, err := reserveAndStart(mainAPI, mainRun, mainVessel.ID); err != nil {
		return err
	}

	// Archiving the east site blocks new writes but keeps reads available.
	archived, err := api.call("POST", "/v1/sites/"+eastSite.ID+"/archive",
		nil, ptrInt64(eastSite.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(archived, 200, "archive east site"); err != nil {
		return err
	}
	var eastAfterArchive siteView
	if err := archived.decode(&eastAfterArchive); err != nil {
		return err
	}
	if eastAfterArchive.State != domain.SiteArchived || eastAfterArchive.Version != 2 {
		return errInvalidWorkflow("archived site has unexpected state or version")
	}

	archivedWrite, err := eastAPI.call("POST", "/formulas",
		createFormulaPayload("Late Formula"), nil)
	if err != nil {
		return err
	}
	if err := requireCode(archivedWrite, 409, string(domain.CodeSiteArchived),
		"write to archived site"); err != nil {
		return err
	}
	archivedRun, err := eastAPI.call("POST", "/runs",
		createRunPayload("RUN-LATE", eastFormula.ID), nil)
	if err != nil {
		return err
	}
	if err := requireCode(archivedRun, 409, string(domain.CodeSiteArchived),
		"create run in archived site"); err != nil {
		return err
	}
	if err := requireFormulaList(eastAPI, eastSite.ID, eastFormula.ID); err != nil {
		return err
	}

	rearchive, err := api.call("POST", "/v1/sites/"+eastSite.ID+"/archive",
		nil, ptrInt64(eastAfterArchive.Version))
	if err != nil {
		return err
	}
	if err := requireCode(rearchive, 409, string(domain.CodeSiteState),
		"archive site twice"); err != nil {
		return err
	}

	// Restart the service against the same data directory.
	if err := h.restart(); err != nil {
		return err
	}
	api = h.api
	mainAPI = api.scoped("/v1/sites/" + mainSite.ID)
	eastAPI = api.scoped("/v1/sites/" + eastSite.ID)

	// Historical runs remain in their original spaces with identical codes.
	mainRuns, err := listRuns(mainAPI)
	if err != nil {
		return err
	}
	if err := requireSingleRun(mainRuns, mainSite.ID, "RUN-SHARED", mainRun.ID,
		domain.RunFermenting); err != nil {
		return fmt.Errorf("main site after restart: %w", err)
	}
	eastRuns, err := listRuns(eastAPI)
	if err != nil {
		return err
	}
	if err := requireSingleRun(eastRuns, eastSite.ID, "RUN-SHARED", eastRun.ID,
		domain.RunPlanned); err != nil {
		return fmt.Errorf("east site after restart: %w", err)
	}

	// Details still cannot cross spaces after the restart.
	crossRunRead, err := mainAPI.call("GET", "/runs/"+eastRun.ID, nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(crossRunRead, 404, string(domain.CodeRunNotFound),
		"read run across sites after restart"); err != nil {
		return err
	}
	crossVesselRead, err := eastAPI.call("GET", "/vessels/"+mainVessel.ID, nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(crossVesselRead, 404, string(domain.CodeVesselNotFound),
		"read vessel across sites after restart"); err != nil {
		return err
	}

	// The archived state and per-site lists survive the restart.
	eastSiteRead, err := api.call("GET", "/v1/sites/"+eastSite.ID, nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(eastSiteRead, 200, "read archived site"); err != nil {
		return err
	}
	var eastSiteAfter siteView
	if err := eastSiteRead.decode(&eastSiteAfter); err != nil {
		return err
	}
	if eastSiteAfter.State != domain.SiteArchived {
		return errInvalidWorkflow("archived site did not stay archived after restart")
	}
	if err := requireFormulaList(mainAPI, mainSite.ID, mainFormula.ID); err != nil {
		return err
	}
	if err := requireFormulaList(eastAPI, eastSite.ID, eastFormula.ID); err != nil {
		return err
	}
	mainVessels, err := listVessels(mainAPI)
	if err != nil {
		return err
	}
	if len(mainVessels) != 1 || mainVessels[0].ID != mainVessel.ID ||
		mainVessels[0].SiteID != mainSite.ID {
		return errInvalidWorkflow("main site vessels changed across restart")
	}
	eastVessels, err := listVessels(eastAPI)
	if err != nil {
		return err
	}
	if len(eastVessels) != 1 || eastVessels[0].ID != eastVessel.ID ||
		eastVessels[0].SiteID != eastSite.ID {
		return errInvalidWorkflow("east site vessels changed across restart")
	}

	// The archived site still rejects writes after the restart.
	archivedAfterRestart, err := eastAPI.call("POST", "/vessels", map[string]any{
		"code":            "VSL-901",
		"capacity_liters": 100.0,
	}, nil)
	if err != nil {
		return err
	}
	if err := requireCode(archivedAfterRestart, 409, string(domain.CodeSiteArchived),
		"write to archived site after restart"); err != nil {
		return err
	}
	return nil
}

func createPlannedRun(scoped *client, code, formulaID string) (runView, error) {
	response, err := scoped.call("POST", "/runs", createRunPayload(code, formulaID), nil)
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(response, 201, "create planned run"); err != nil {
		return runView{}, err
	}
	var run runView
	if err := response.decode(&run); err != nil {
		return runView{}, err
	}
	return run, nil
}

func reserveAndStart(scoped *client, run runView, vesselID string) (pairView, error) {
	reserveResponse, err := scoped.call("POST", "/runs/"+run.ID+"/reserve",
		map[string]any{"vessel_id": vesselID}, ptrInt64(run.Version))
	if err != nil {
		return pairView{}, err
	}
	if err := requireStatus(reserveResponse, 200, "reserve same-site vessel"); err != nil {
		return pairView{}, err
	}
	var reserved pairView
	if err := reserveResponse.decode(&reserved); err != nil {
		return pairView{}, err
	}
	startResponse, err := scoped.call("POST", "/runs/"+run.ID+"/start",
		nil, ptrInt64(reserved.Run.Version))
	if err != nil {
		return pairView{}, err
	}
	if err := requireStatus(startResponse, 200, "start same-site run"); err != nil {
		return pairView{}, err
	}
	var started pairView
	if err := startResponse.decode(&started); err != nil {
		return pairView{}, err
	}
	if err := requireRunState(started.Run, domain.RunFermenting); err != nil {
		return pairView{}, err
	}
	return started, nil
}

func requireFormulaList(scoped *client, siteID, formulaID string) error {
	response, err := scoped.call("GET", "/formulas", nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(response, 200, "list formulas"); err != nil {
		return err
	}
	var list formulaListView
	if err := response.decode(&list); err != nil {
		return err
	}
	if len(list.Formulas) != 1 {
		return errInvalidWorkflow("formula list leaked across sites")
	}
	if list.Formulas[0].ID != formulaID || list.Formulas[0].SiteID != siteID {
		return errInvalidWorkflow("formula list returned a foreign formula")
	}
	return nil
}

func listRuns(scoped *client) ([]runView, error) {
	response, err := scoped.call("GET", "/runs", nil, nil)
	if err != nil {
		return nil, err
	}
	if err := requireStatus(response, 200, "list runs"); err != nil {
		return nil, err
	}
	var list runListView
	if err := response.decode(&list); err != nil {
		return nil, err
	}
	return list.Runs, nil
}

func listVessels(scoped *client) ([]vesselView, error) {
	response, err := scoped.call("GET", "/vessels", nil, nil)
	if err != nil {
		return nil, err
	}
	if err := requireStatus(response, 200, "list vessels"); err != nil {
		return nil, err
	}
	var list vesselListView
	if err := response.decode(&list); err != nil {
		return nil, err
	}
	return list.Vessels, nil
}

func requireSingleRun(
	runs []runView,
	siteID string,
	code string,
	id string,
	state domain.RunState,
) error {
	if len(runs) != 1 {
		return errInvalidWorkflow("run list leaked across sites")
	}
	run := runs[0]
	if run.ID != id || run.Code != code || run.SiteID != siteID {
		return errInvalidWorkflow("run list returned a foreign run")
	}
	if run.State != state {
		return fmt.Errorf("expected run state %q, got %q", state, run.State)
	}
	return nil
}
