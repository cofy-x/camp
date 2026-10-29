package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/runs"
)

type SettingsService interface {
	ModelSettings() configuration.View
	UpdateModelSettings(context.Context, configuration.Update) (configuration.View, error)
	EnvironmentSettings() runs.EnvironmentSettings
	StorageSettings() runs.StorageSettings
	CheckReadiness(context.Context, bool) (runs.Readiness, error)
}

func (a *API) EnableSettings(service SettingsService, policy configuration.ImportPolicy) {
	a.settings = service
	a.importPolicy = policy
}
func (a *API) settingsRoutes(mux *http.ServeMux) {
	register := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, a.admin(func(w http.ResponseWriter, r *http.Request) {
			if a.settings == nil {
				writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "Settings are not initialized.")
				return
			}
			handler(w, r)
		}))
	}
	register("GET /api/settings/models", func(w http.ResponseWriter, r *http.Request) { a.write(w, http.StatusOK, a.settings.ModelSettings()) })
	register("PATCH /api/settings/models", func(w http.ResponseWriter, r *http.Request) {
		var input configuration.Update
		if !decode(w, r, &input, 16384) {
			return
		}
		value, err := a.settings.UpdateModelSettings(r.Context(), input)
		a.settingsRespond(w, r, value, err)
	})
	register("GET /api/settings/environments", func(w http.ResponseWriter, r *http.Request) {
		a.write(w, http.StatusOK, a.settings.EnvironmentSettings())
	})
	register("GET /api/settings/storage", func(w http.ResponseWriter, r *http.Request) {
		a.write(w, http.StatusOK, struct {
			runs.StorageSettings
			configuration.ImportPolicy
		}{a.settings.StorageSettings(), a.importPolicy})
	})
	register("GET /api/settings/readiness", func(w http.ResponseWriter, r *http.Request) {
		value, err := a.settings.CheckReadiness(r.Context(), false)
		a.settingsRespond(w, r, value, err)
	})
	register("POST /api/settings/readiness/recheck", func(w http.ResponseWriter, r *http.Request) {
		var input struct{}
		if !decode(w, r, &input, 1024) {
			return
		}
		value, err := a.settings.CheckReadiness(r.Context(), true)
		a.settingsRespond(w, r, value, err)
	})
}
func (a *API) settingsRespond(w http.ResponseWriter, r *http.Request, value any, err error) {
	var problem *configuration.Error
	if errors.As(err, &problem) {
		status := http.StatusBadRequest
		if problem.Code == "settings_audit_failed" {
			status = http.StatusInternalServerError
		}
		if problem.Code == "settings_overridden" || problem.Code == "runtime_busy" {
			status = http.StatusConflict
		}
		if problem.Code == "import_denied" || problem.Code == "import_disabled" {
			status = http.StatusForbidden
		}
		writeError(w, status, problem.Code, problem.Message)
		return
	}
	a.respond(w, r, value, err)
}
