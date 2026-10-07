package workflowcheck

import (
	"sync"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

// cellarNamespaces proves that two sites sharing one deployment keep fully
// independent namespaces: identical names and codes never collide, queries
// and exports stay inside their own cellar, cross-cellar references are
// rejected, archiving is atomic against concurrent writes, and a restart
// leaves every run in its original cellar.
func cellarNamespaces(h *harness) error {
	api := h.api

	cellarA, err := ensureCellar(api, "Main Plant")
	if err != nil {
		return err
	}
	cellarB, err := ensureCellar(api, "East Branch")
	if err != nil {
		return err
	}

	duplicateCellar, err := api.call("POST", "/v1/cellars",
		map[string]any{"name": "  main   plant  "}, nil)
	if err != nil {
		return err
	}
	if err := requireCode(duplicateCellar, 409, string(domain.CodeCellarNameConflict),
		"create duplicate cellar"); err != nil {
		return err
	}

	// Identical names and codes are accepted in independent cellars.
	formulaA, err := ensureApprovedFormula(api, cellarA.ID, "Shared Recipe")
	if err != nil {
		return err
	}
	formulaB, err := ensureApprovedFormula(api, cellarB.ID, "Shared Recipe")
	if err != nil {
		return err
	}
	duplicateFormula, err := api.call("POST", cellarPath(cellarA.ID, "/formulas"),
		createFormulaPayload("shared   recipe"), nil)
	if err != nil {
		return err
	}
	if err := requireCode(duplicateFormula, 409, string(domain.CodeFormulaNameConflict),
		"duplicate formula name inside one cellar"); err != nil {
		return err
	}

	vesselA, err := createCheckVessel(api, cellarA.ID, "VSL-100", 240)
	if err != nil {
		return err
	}
	vesselB, err := createCheckVessel(api, cellarB.ID, "VSL-100", 240)
	if err != nil {
		return err
	}
	duplicateVessel, err := api.call("POST", cellarPath(cellarA.ID, "/vessels"),
		map[string]any{"code": "vsl-100", "capacity_liters": 120.0}, nil)
	if err != nil {
		return err
	}
	if err := requireCode(duplicateVessel, 409, string(domain.CodeVesselConflict),
		"duplicate vessel code inside one cellar"); err != nil {
		return err
	}

	runA, err := createCheckRun(api, cellarA.ID, "RUN-SHARED", formulaA.ID)
	if err != nil {
		return err
	}
	runB, err := createCheckRun(api, cellarB.ID, "RUN-SHARED", formulaB.ID)
	if err != nil {
		return err
	}
	duplicateRun, err := api.call("POST", cellarPath(cellarA.ID, "/runs"),
		createRunPayload("run-shared", formulaA.ID), nil)
	if err != nil {
		return err
	}
	if err := requireCode(duplicateRun, 409, string(domain.CodeRunConflict),
		"duplicate run code inside one cellar"); err != nil {
		return err
	}

	// Lists only contain the local cellar's data.
	if err := requireListIsolation(api, cellarA.ID, formulaA.ID, vesselA.ID, runA.ID); err != nil {
		return err
	}
	if err := requireListIsolation(api, cellarB.ID, formulaB.ID, vesselB.ID, runB.ID); err != nil {
		return err
	}

	// Details from another cellar are invisible, not leaked.
	if err := requireNotFound(api, cellarA.ID, formulaB.ID, vesselB.ID, runB.ID); err != nil {
		return err
	}
	if err := requireNotFound(api, cellarB.ID, formulaA.ID, vesselA.ID, runA.ID); err != nil {
		return err
	}

	// A run cannot reference another cellar's formula or vessel.
	crossFormula, err := api.call("POST", cellarPath(cellarB.ID, "/runs"),
		createRunPayload("RUN-CROSS-FORMULA", formulaA.ID), nil)
	if err != nil {
		return err
	}
	if err := requireCode(crossFormula, 404, string(domain.CodeFormulaNotFound),
		"create run with another cellar's formula"); err != nil {
		return err
	}
	crossVessel, err := api.call("POST", cellarPath(cellarB.ID, "/runs/"+runB.ID+"/reserve"),
		map[string]any{"vessel_id": vesselA.ID}, ptrInt64(runB.Version))
	if err != nil {
		return err
	}
	if err := requireCode(crossVessel, 404, string(domain.CodeVesselNotFound),
		"reserve another cellar's vessel"); err != nil {
		return err
	}

	// Archiving is blocked while the cellar holds in-flight work.
	if _, err := transitionRun(api, cellarA.ID, runA, vesselA.ID); err != nil {
		return err
	}
	busyArchive, err := api.call("POST", cellarPath(cellarA.ID, "/archive"), nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(busyArchive, 409, string(domain.CodeCellarInUse),
		"archive cellar with an open run"); err != nil {
		return err
	}
	aborted, err := abortRun(api, cellarA.ID, runA.ID, 3)
	if err != nil {
		return err
	}
	if aborted.Vessel.ActiveRunID == nil {
		return errInvalidWorkflow("cleaning vessel lost its run association")
	}
	boundArchive, err := api.call("POST", cellarPath(cellarA.ID, "/archive"), nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(boundArchive, 409, string(domain.CodeCellarInUse),
		"archive cellar with a vessel still bound to a run"); err != nil {
		return err
	}
	cleaned, err := api.call("POST",
		cellarPath(cellarA.ID, "/vessels/"+vesselA.ID+"/finish-cleaning"),
		nil, ptrInt64(aborted.Vessel.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(cleaned, 200, "finish cleaning before archive"); err != nil {
		return err
	}
	archiveResponse, err := api.call("POST", cellarPath(cellarA.ID, "/archive"), nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(archiveResponse, 200, "archive quiet cellar"); err != nil {
		return err
	}
	var archivedCellar cellarView
	if err := archiveResponse.decode(&archivedCellar); err != nil {
		return err
	}
	if archivedCellar.State != domain.CellarArchived {
		return errInvalidWorkflow("cellar did not reach the archived state")
	}

	// An archived cellar is read-only history.
	frozenFormula, err := api.call("POST", cellarPath(cellarA.ID, "/formulas"),
		createFormulaPayload("Frozen Recipe"), nil)
	if err != nil {
		return err
	}
	if err := requireCode(frozenFormula, 409, string(domain.CodeCellarState),
		"create formula in archived cellar"); err != nil {
		return err
	}
	frozenRun, err := api.call("POST", cellarPath(cellarA.ID, "/runs"),
		createRunPayload("RUN-FROZEN", formulaA.ID), nil)
	if err != nil {
		return err
	}
	if err := requireCode(frozenRun, 409, string(domain.CodeCellarState),
		"create run in archived cellar"); err != nil {
		return err
	}
	frozenVessel, err := api.call("POST", cellarPath(cellarA.ID, "/vessels"),
		map[string]any{"code": "VSL-900", "capacity_liters": 100.0}, nil)
	if err != nil {
		return err
	}
	if err := requireCode(frozenVessel, 409, string(domain.CodeCellarState),
		"create vessel in archived cellar"); err != nil {
		return err
	}
	reArchive, err := api.call("POST", cellarPath(cellarA.ID, "/archive"), nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(reArchive, 409, string(domain.CodeCellarState),
		"archive cellar twice"); err != nil {
		return err
	}
	historyResponse, err := api.call("GET", cellarPath(cellarA.ID, "/runs/"+runA.ID), nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(historyResponse, 200, "read run in archived cellar"); err != nil {
		return err
	}

	// Concurrent identical creates leave exactly one winner per cellar.
	cellarC, err := ensureCellar(api, "Concurrent West")
	if err != nil {
		return err
	}
	cellarD, err := ensureCellar(api, "Concurrent East")
	if err != nil {
		return err
	}
	if err := raceDuplicateCreates(api, cellarC.ID, cellarD.ID); err != nil {
		return err
	}

	// Archiving raced against a write produces exactly one winner and never
	// a half-finished cross-space state.
	cellarE, err := ensureCellar(api, "Race Cellar")
	if err != nil {
		return err
	}
	formulaE, err := ensureApprovedFormula(api, cellarE.ID, "Race Formula")
	if err != nil {
		return err
	}
	if err := raceArchiveAgainstWrite(api, cellarE.ID, formulaE.ID); err != nil {
		return err
	}

	// A restart keeps every run in its original cellar.
	if err := h.restart(); err != nil {
		return err
	}
	api = h.api
	if err := requireListIsolation(api, cellarA.ID, formulaA.ID, vesselA.ID, runA.ID); err != nil {
		return err
	}
	if err := requireListIsolation(api, cellarB.ID, formulaB.ID, vesselB.ID, runB.ID); err != nil {
		return err
	}
	if err := requireNotFound(api, cellarB.ID, formulaA.ID, vesselA.ID, runA.ID); err != nil {
		return err
	}

	restartedA, err := api.call("GET", cellarPath(cellarA.ID, "/runs/"+runA.ID), nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(restartedA, 200, "read archived run after restart"); err != nil {
		return err
	}
	var runAAfter runView
	if err := restartedA.decode(&runAAfter); err != nil {
		return err
	}
	if runAAfter.CellarID != cellarA.ID || runAAfter.FormulaID != formulaA.ID ||
		runAAfter.State != domain.RunAborted {
		return errInvalidWorkflow("restart moved or altered a historical run")
	}
	restartedB, err := api.call("GET", cellarPath(cellarB.ID, "/runs/"+runB.ID), nil, nil)
	if err != nil {
		return err
	}
	var runBAfter runView
	if err := restartedB.decode(&runBAfter); err != nil {
		return err
	}
	if runBAfter.CellarID != cellarB.ID || runBAfter.FormulaID != formulaB.ID {
		return errInvalidWorkflow("restart crossed runs that share a code")
	}

	stillArchived, err := api.call("GET", "/v1/cellars/"+cellarA.ID, nil, nil)
	if err != nil {
		return err
	}
	var cellarAAfter cellarView
	if err := stillArchived.decode(&cellarAAfter); err != nil {
		return err
	}
	if cellarAAfter.State != domain.CellarArchived {
		return errInvalidWorkflow("restart resurrected an archived cellar")
	}
	frozenAfterRestart, err := api.call("POST", cellarPath(cellarA.ID, "/formulas"),
		createFormulaPayload("Post Restart Recipe"), nil)
	if err != nil {
		return err
	}
	if err := requireCode(frozenAfterRestart, 409, string(domain.CodeCellarState),
		"write to archived cellar after restart"); err != nil {
		return err
	}

	// Exports stay inside their own cellar even when codes are identical.
	if err := requireExportIsolation(api, cellarA.ID,
		[]string{formulaA.ID}, []string{vesselA.ID}, []string{runA.ID}); err != nil {
		return err
	}
	if err := requireExportIsolation(api, cellarB.ID,
		[]string{formulaB.ID}, []string{vesselB.ID}, []string{runB.ID}); err != nil {
		return err
	}
	return nil
}

func createCheckRun(api *client, cellarID, code, formulaID string) (runView, error) {
	response, err := api.call("POST", cellarPath(cellarID, "/runs"),
		createRunPayload(code, formulaID), nil)
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(response, 201, "create run"); err != nil {
		return runView{}, err
	}
	var run runView
	if err := response.decode(&run); err != nil {
		return runView{}, err
	}
	if run.CellarID != cellarID {
		return runView{}, errInvalidWorkflow("created run landed in the wrong cellar")
	}
	return run, nil
}

// transitionRun reserves and starts a run, returning the advanced view.
func transitionRun(api *client, cellarID string, run runView, vesselID string) (runView, error) {
	reserveResponse, err := api.call("POST", cellarPath(cellarID, "/runs/"+run.ID+"/reserve"),
		map[string]any{"vessel_id": vesselID}, ptrInt64(run.Version))
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(reserveResponse, 200, "reserve run before archive test"); err != nil {
		return runView{}, err
	}
	var reserved pairView
	if err := reserveResponse.decode(&reserved); err != nil {
		return runView{}, err
	}
	startResponse, err := api.call("POST", cellarPath(cellarID, "/runs/"+run.ID+"/start"),
		nil, ptrInt64(reserved.Run.Version))
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(startResponse, 200, "start run before archive test"); err != nil {
		return runView{}, err
	}
	var started pairView
	if err := startResponse.decode(&started); err != nil {
		return runView{}, err
	}
	return started.Run, nil
}

func abortRun(api *client, cellarID, runID string, version int64) (pairView, error) {
	response, err := api.call("POST", cellarPath(cellarID, "/runs/"+runID+"/abort"),
		nil, ptrInt64(version))
	if err != nil {
		return pairView{}, err
	}
	if err := requireStatus(response, 200, "abort run"); err != nil {
		return pairView{}, err
	}
	var pair pairView
	if err := response.decode(&pair); err != nil {
		return pairView{}, err
	}
	if err := requireRunState(pair.Run, domain.RunAborted); err != nil {
		return pairView{}, err
	}
	if err := requireVesselState(pair.Vessel, domain.VesselCleaning); err != nil {
		return pairView{}, err
	}
	return pair, nil
}

// requireListIsolation proves a cellar's lists contain exactly its own
// formula, vessel, and run, even though another cellar uses the same codes.
func requireListIsolation(api *client, cellarID, formulaID, vesselID, runID string) error {
	formulasResponse, err := api.call("GET", cellarPath(cellarID, "/formulas"), nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(formulasResponse, 200, "list formulas"); err != nil {
		return err
	}
	var formulaList struct {
		Formulas []formulaView `json:"formulas"`
	}
	if err := formulasResponse.decode(&formulaList); err != nil {
		return err
	}
	if len(formulaList.Formulas) != 1 || formulaList.Formulas[0].ID != formulaID ||
		formulaList.Formulas[0].CellarID != cellarID {
		return errInvalidWorkflow("formula list is not isolated to its cellar")
	}

	vesselsResponse, err := api.call("GET", cellarPath(cellarID, "/vessels"), nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(vesselsResponse, 200, "list vessels"); err != nil {
		return err
	}
	var vesselList struct {
		Vessels []vesselView `json:"vessels"`
	}
	if err := vesselsResponse.decode(&vesselList); err != nil {
		return err
	}
	if len(vesselList.Vessels) != 1 || vesselList.Vessels[0].ID != vesselID ||
		vesselList.Vessels[0].CellarID != cellarID {
		return errInvalidWorkflow("vessel list is not isolated to its cellar")
	}

	runsResponse, err := api.call("GET", cellarPath(cellarID, "/runs"), nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(runsResponse, 200, "list runs"); err != nil {
		return err
	}
	var runList struct {
		Runs []runView `json:"runs"`
	}
	if err := runsResponse.decode(&runList); err != nil {
		return err
	}
	if len(runList.Runs) != 1 || runList.Runs[0].ID != runID ||
		runList.Runs[0].CellarID != cellarID {
		return errInvalidWorkflow("run list is not isolated to its cellar")
	}
	return nil
}

// requireNotFound proves another cellar's aggregates are invisible here.
func requireNotFound(api *client, cellarID, formulaID, vesselID, runID string) error {
	formulaResponse, err := api.call("GET", cellarPath(cellarID, "/formulas/"+formulaID), nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(formulaResponse, 404, string(domain.CodeFormulaNotFound),
		"read another cellar's formula"); err != nil {
		return err
	}
	vesselResponse, err := api.call("GET", cellarPath(cellarID, "/vessels/"+vesselID), nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(vesselResponse, 404, string(domain.CodeVesselNotFound),
		"read another cellar's vessel"); err != nil {
		return err
	}
	runResponse, err := api.call("GET", cellarPath(cellarID, "/runs/"+runID), nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(runResponse, 404, string(domain.CodeRunNotFound),
		"read another cellar's run"); err != nil {
		return err
	}
	return nil
}

// requireExportIsolation proves an export contains exactly the expected
// aggregates and nothing from a cellar that reuses the same codes.
func requireExportIsolation(
	api *client,
	cellarID string,
	formulaIDs []string,
	vesselIDs []string,
	runIDs []string,
) error {
	response, err := api.call("GET", cellarPath(cellarID, "/export"), nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(response, 200, "export cellar"); err != nil {
		return err
	}
	var exported struct {
		Cellar   cellarView    `json:"cellar"`
		Formulas []formulaView `json:"formulas"`
		Vessels  []vesselView  `json:"vessels"`
		Runs     []runView     `json:"runs"`
	}
	if err := response.decode(&exported); err != nil {
		return err
	}
	if exported.Cellar.ID != cellarID {
		return errInvalidWorkflow("export returned the wrong cellar")
	}
	if err := requireExportSet("formula", cellarID, formulaIDs, len(exported.Formulas),
		func(i int) (string, string) {
			return exported.Formulas[i].ID, exported.Formulas[i].CellarID
		}); err != nil {
		return err
	}
	if err := requireExportSet("vessel", cellarID, vesselIDs, len(exported.Vessels),
		func(i int) (string, string) {
			return exported.Vessels[i].ID, exported.Vessels[i].CellarID
		}); err != nil {
		return err
	}
	return requireExportSet("run", cellarID, runIDs, len(exported.Runs),
		func(i int) (string, string) {
			return exported.Runs[i].ID, exported.Runs[i].CellarID
		})
}

func requireExportSet(
	kind string,
	cellarID string,
	expectedIDs []string,
	actualLen int,
	lookup func(i int) (id string, cellar string),
) error {
	if actualLen != len(expectedIDs) {
		return errInvalidWorkflow("export contains foreign or missing " + kind + " entries")
	}
	remaining := make(map[string]int, len(expectedIDs))
	for _, id := range expectedIDs {
		remaining[id]++
	}
	for i := 0; i < actualLen; i++ {
		id, cellar := lookup(i)
		if cellar != cellarID {
			return errInvalidWorkflow("export contains a " + kind + " from another cellar")
		}
		if remaining[id] == 0 {
			return errInvalidWorkflow("export contains an unexpected " + kind)
		}
		remaining[id]--
	}
	return nil
}

// raceDuplicateCreates fires identical formula creates at two cellars and
// requires exactly one winner per cellar with no cross-cellar interference.
func raceDuplicateCreates(api *client, cellarIDs ...string) error {
	const perCellar = 4
	type outcome struct {
		cellar   string
		response response
		err      error
	}
	results := make(chan outcome, perCellar*len(cellarIDs))
	var wg sync.WaitGroup
	for _, cellarID := range cellarIDs {
		for i := 0; i < perCellar; i++ {
			wg.Add(1)
			go func(target string) {
				defer wg.Done()
				resp, err := api.call("POST", cellarPath(target, "/formulas"),
					createFormulaPayload("Concurrent Recipe"), nil)
				results <- outcome{cellar: target, response: resp, err: err}
			}(cellarID)
		}
	}
	wg.Wait()
	close(results)

	winners := map[string]int{}
	for result := range results {
		if result.err != nil {
			return result.err
		}
		if result.response.status == 201 {
			winners[result.cellar]++
			continue
		}
		if err := requireCode(result.response, 409, string(domain.CodeFormulaNameConflict),
			"concurrent duplicate create"); err != nil {
			return err
		}
	}
	for _, cellarID := range cellarIDs {
		if winners[cellarID] != 1 {
			return errInvalidWorkflow("concurrent creates did not leave exactly one formula per cellar")
		}
	}
	return nil
}

// raceArchiveAgainstWrite runs an archive and a run creation concurrently.
// Exactly one may win, and the resulting export must be consistent: an
// archived cellar never contains an open run.
func raceArchiveAgainstWrite(api *client, cellarID, formulaID string) error {
	var wg sync.WaitGroup
	wg.Add(2)
	var archiveResponse, runResponse response
	var archiveErr, runErr error
	go func() {
		defer wg.Done()
		archiveResponse, archiveErr = api.call("POST",
			cellarPath(cellarID, "/archive"), nil, nil)
	}()
	go func() {
		defer wg.Done()
		runResponse, runErr = api.call("POST", cellarPath(cellarID, "/runs"),
			createRunPayload("RUN-RACE", formulaID), nil)
	}()
	wg.Wait()
	if archiveErr != nil {
		return archiveErr
	}
	if runErr != nil {
		return runErr
	}

	archived := archiveResponse.status == 200
	runCreated := runResponse.status == 201
	if archived == runCreated {
		return errInvalidWorkflow("archive and run creation must have exactly one winner")
	}
	if archived {
		if err := requireCode(runResponse, 409, string(domain.CodeCellarState),
			"create run while archiving"); err != nil {
			return err
		}
	} else {
		if err := requireCode(archiveResponse, 409, string(domain.CodeCellarInUse),
			"archive while a run opens"); err != nil {
			return err
		}
	}

	exportResponse, err := api.call("GET", cellarPath(cellarID, "/export"), nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(exportResponse, 200, "export raced cellar"); err != nil {
		return err
	}
	var exported struct {
		Cellar cellarView `json:"cellar"`
		Runs   []runView  `json:"runs"`
	}
	if err := exportResponse.decode(&exported); err != nil {
		return err
	}
	if exported.Cellar.State == domain.CellarArchived {
		for _, run := range exported.Runs {
			if run.State != domain.RunCompleted && run.State != domain.RunAborted {
				return errInvalidWorkflow("archived cellar contains an open run")
			}
		}
		return nil
	}
	if !runCreated || len(exported.Runs) != 1 || exported.Runs[0].Code != "RUN-RACE" {
		return errInvalidWorkflow("active cellar lost the run that won the race")
	}
	return nil
}
