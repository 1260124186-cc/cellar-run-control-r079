package httpapi

import (
	"net/http"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/service"
)

type createSiteRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

func registerSiteRoutes(mux *http.ServeMux, app *service.Service) {
	mux.HandleFunc("POST /v1/sites", func(response http.ResponseWriter, request *http.Request) {
		body, err := decodeJSON[createSiteRequest](response, request)
		if !handleDecode(response, err) {
			return
		}
		site, err := app.CreateSite(service.CreateSiteInput{
			Code: body.Code,
			Name: body.Name,
		})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, site)
	})

	mux.HandleFunc("GET /v1/sites", func(response http.ResponseWriter, _ *http.Request) {
		sites, err := app.ListSites()
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"sites": sites})
	})

	mux.HandleFunc("GET /v1/sites/{id}", func(response http.ResponseWriter, request *http.Request) {
		site, err := app.GetSite(request.PathValue("id"))
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, site)
	})

	mux.HandleFunc("POST /v1/sites/{id}/archive", func(response http.ResponseWriter, request *http.Request) {
		expected, err := expectedVersion(request)
		if !handleDecode(response, err) {
			return
		}
		site, err := app.ArchiveSite(request.PathValue("id"),
			service.VersionInput{ExpectedVersion: expected})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, site)
	})
}
