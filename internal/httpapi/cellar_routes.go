package httpapi

import (
	"net/http"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/service"
)

type createCellarRequest struct {
	Name string `json:"name"`
}

func registerCellarRoutes(mux *http.ServeMux, app *service.Service) {
	mux.HandleFunc("POST /v1/cellars", func(response http.ResponseWriter, request *http.Request) {
		body, err := decodeJSON[createCellarRequest](response, request)
		if !handleDecode(response, err) {
			return
		}
		cellar, err := app.CreateCellar(service.CreateCellarInput{Name: body.Name})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, cellar)
	})

	mux.HandleFunc("GET /v1/cellars", func(response http.ResponseWriter, _ *http.Request) {
		cellars, err := app.ListCellars()
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"cellars": cellars})
	})

	mux.HandleFunc("GET /v1/cellars/{cellar}", func(response http.ResponseWriter, request *http.Request) {
		cellar, err := app.GetCellar(request.PathValue("cellar"))
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, cellar)
	})

	mux.HandleFunc("POST /v1/cellars/{cellar}/archive", func(response http.ResponseWriter, request *http.Request) {
		expected, err := expectedVersion(request)
		if !handleDecode(response, err) {
			return
		}
		cellar, err := app.ArchiveCellar(request.PathValue("cellar"),
			service.VersionInput{ExpectedVersion: expected})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, cellar)
	})

	mux.HandleFunc("GET /v1/cellars/{cellar}/export", func(response http.ResponseWriter, request *http.Request) {
		export, err := app.ExportCellar(request.PathValue("cellar"))
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{
			"cellar":   export.Cellar,
			"formulas": export.Formulas,
			"vessels":  export.Vessels,
			"runs":     export.Runs,
		})
	})
}
