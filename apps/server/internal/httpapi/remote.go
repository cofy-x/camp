package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/remoteruns"
	"github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// EnableRemote connects browser management to the same durable scheduler used by
// daemon traffic. Authentication and Origin validation stay in the browser API.
func (a *API) EnableRemote(manager *remoteruns.Manager) { a.remote = manager }

type runnerCapabilitiesView struct {
	Platform       string                          `json:"platform,omitempty"`
	Workspace      string                          `json:"workspace,omitempty"`
	Sandbox        string                          `json:"sandbox,omitempty"`
	Checkpoint     string                          `json:"checkpoint,omitempty"`
	Readiness      *runnerprotocol.ReadinessReport `json:"readiness,omitempty"`
	Executor       string                          `json:"executor"`
	Ready          bool                            `json:"ready"`
	Reason         string                          `json:"reason,omitempty"`
	Slots          int                             `json:"slots"`
	DSHVersion     string                          `json:"dshVersion,omitempty"`
	LayerFSVersion string                          `json:"layerfsVersion,omitempty"`
}

type browserRunner struct {
	EarlierTaskRunID       string                 `json:"earlierTaskRunId,omitempty"`
	EarlierTaskSucceededAt string                 `json:"earlierTaskSucceededAt,omitempty"`
	FirstTaskRunID         string                 `json:"firstTaskRunId"`
	FirstTaskSucceededAt   string                 `json:"firstTaskSucceededAt"`
	ModelConfigured        bool                   `json:"modelConfigured"`
	ModelCheckedAt         string                 `json:"modelCheckedAt"`
	OwnerUserID            string                 `json:"ownerUserId"`
	TeamShared             bool                   `json:"teamShared"`
	ID                     string                 `json:"id"`
	Name                   string                 `json:"name"`
	Enabled                bool                   `json:"enabled"`
	Revoked                bool                   `json:"revoked"`
	Online                 bool                   `json:"online"`
	LastSeenAt             string                 `json:"lastSeenAt"`
	Runtime                string                 `json:"runtime"`
	Slots                  int                    `json:"slots"`
	Capabilities           runnerCapabilitiesView `json:"capabilities"`
}

func runnerForBrowser(runner storage.Runner, online bool) browserRunner {
	var capabilities runnerCapabilitiesView
	_ = json.Unmarshal(runner.Capabilities, &capabilities)
	return browserRunner{OwnerUserID: runner.OwnerUserID, TeamShared: runner.TeamShared, ID: runner.ID, Name: runner.Name, Enabled: runner.Enabled,
		Revoked: runner.Revoked, Online: online, LastSeenAt: runner.LastSeenAt,
		Runtime: runner.Runtime, Slots: runner.Slots, Capabilities: capabilities}
}

func (a *API) remoteRoutes(mux *http.ServeMux) {
	register := func(pattern string, admin bool, handler http.HandlerFunc) {
		protected := func(w http.ResponseWriter, r *http.Request) {
			user, authenticated := storage.UserFromContext(r.Context())
			if !authenticated {
				writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to Camp.")
				return
			}
			if admin && user.Role != "admin" {
				writeError(w, http.StatusForbidden, "forbidden", "An administrator is required.")
				return
			}
			if a.remote == nil {
				writeError(w, http.StatusServiceUnavailable, "remote_unavailable", "Remote execution is not initialized.")
				return
			}
			handler(w, r)
		}
		mux.HandleFunc(pattern, protected)
	}
	register("GET /api/execution-defaults", false, func(w http.ResponseWriter, r *http.Request) {
		value, err := a.store.GetExecutionDefaults(r.Context())
		a.respond(w, r, value, err)
	})
	register("PATCH /api/execution-defaults", false, func(w http.ResponseWriter, r *http.Request) {
		var input storage.ExecutionDefaultInput
		if !decode(w, r, &input, 4096) {
			return
		}
		value, err := a.store.SetExecutionDefault(r.Context(), input)
		a.respond(w, r, value, err)
	})
	register("GET /api/runners/{id}/agents", false, func(w http.ResponseWriter, r *http.Request) {
		values, err := a.store.ListRunnerAgentGrants(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"agentIds": values}, err)
	})
	for _, method := range []string{"PUT", "DELETE"} {
		register(method+" /api/runners/{id}/agents/{agentId}", false, func(w http.ResponseWriter, r *http.Request) {
			var input struct{}
			if !decode(w, r, &input, 1024) {
				return
			}
			values, err := a.store.SetRunnerAgentGrant(r.Context(), r.PathValue("id"), r.PathValue("agentId"), r.Method == "PUT")
			if err == nil {
				a.remote.Wake()
			}
			a.respond(w, r, map[string]any{"agentIds": values}, err)
		})
	}
	register("GET /api/runners", false, func(w http.ResponseWriter, r *http.Request) {
		runners, err := a.remote.ListRunners(r.Context())
		out := make([]browserRunner, 0, len(runners))
		for _, runner := range runners {
			view := runnerForBrowser(runner.Runner, runner.Online)
			view.EarlierTaskRunID = runner.EarlierTaskRunID
			view.EarlierTaskSucceededAt = runner.EarlierTaskSucceededAt
			view.FirstTaskRunID = runner.FirstTaskRunID
			view.FirstTaskSucceededAt = runner.FirstTaskSucceededAt
			view.ModelConfigured = runner.ModelConfigured
			view.ModelCheckedAt = runner.ModelCheckedAt
			out = append(out, view)
		}
		a.respond(w, r, map[string]any{"runners": out}, err)
	})
	register("POST /api/runner-enrollments", false, func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &input, 4096) {
			return
		}
		enrollment, token, err := a.store.CreateRunnerEnrollment(r.Context(), input.Name, time.Now().Add(time.Hour))
		if err != nil {
			a.fail(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		a.write(w, http.StatusOK, map[string]any{"enrollment": enrollment, "token": token})
	})
	register("GET /api/runner-enrollments", false, func(w http.ResponseWriter, r *http.Request) {
		values, err := a.store.ListRunnerEnrollments(r.Context())
		a.respond(w, r, map[string]any{"enrollments": values}, err)
	})
	register("POST /api/runner-enrollments/{id}/revoke", false, func(w http.ResponseWriter, r *http.Request) {
		var input struct{}
		if !decode(w, r, &input, 1024) {
			return
		}
		err := a.store.RevokeRunnerEnrollment(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{}, err)
	})
	register("PATCH /api/runners/{id}", false, a.updateRunner)
	register("GET /api/runners/{id}/sources", false, func(w http.ResponseWriter, r *http.Request) {
		sources, err := a.store.RunnerSources(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"sources": sources}, err)
	})
	register("PUT /api/rooms/{id}/runner", true, func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			RunnerID string `json:"runnerId"`
			Revision *int64 `json:"revision"`
		}
		if !decode(w, r, &input, 4096) {
			return
		}
		if input.Revision == nil || *input.Revision < 0 {
			writeError(w, http.StatusBadRequest, "invalid_runner", "Include the current Room runner revision.")
			return
		}
		room, err := a.store.SetRoomRunner(r.Context(), r.PathValue("id"), input.RunnerID, *input.Revision)
		if err == nil {
			a.remote.Wake()
		}
		a.respond(w, r, map[string]any{"room": room}, err)
	})
	register("POST /api/source-imports", false, func(w http.ResponseWriter, r *http.Request) {
		var input storage.SourceImportInput
		if !decode(w, r, &input, 8192) {
			return
		}
		value, err := a.store.CreateSourceImport(r.Context(), input)
		if err == nil {
			a.remote.Wake()
		}
		a.respond(w, r, map[string]any{"sourceImport": value}, err)
	})
	register("GET /api/source-imports", false, func(w http.ResponseWriter, r *http.Request) {
		values, err := a.store.ListSourceImports(r.Context())
		a.respond(w, r, map[string]any{"sourceImports": values}, err)
	})
	register("GET /api/source-imports/{id}", false, func(w http.ResponseWriter, r *http.Request) {
		value, err := a.store.GetSourceImport(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"sourceImport": value}, err)
	})
	register("POST /api/source-imports/{id}/cancel", false, func(w http.ResponseWriter, r *http.Request) {
		var input struct{}
		if !decode(w, r, &input, 1024) {
			return
		}
		value, err := a.store.CancelSourceImport(r.Context(), r.PathValue("id"))
		if err == nil {
			a.remote.Wake()
		}
		a.respond(w, r, map[string]any{"sourceImport": value}, err)
	})
}

func (a *API) updateRunner(w http.ResponseWriter, r *http.Request) {
	var input storage.RunnerUpdate
	if !decode(w, r, &input, 4096) {
		return
	}
	runner, err := a.store.UpdateRunner(r.Context(), r.PathValue("id"), input)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !runner.Enabled || runner.Revoked {
		a.remote.StopRunner(runner.ID)
	} else {
		a.remote.Wake()
	}
	a.write(w, http.StatusOK, map[string]any{"runner": runnerForBrowser(runner, a.remote.IsOnline(runner.ID))})
}
