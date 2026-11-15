package httpapi

import (
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/githubdelivery"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func (a *API) EnableGitHubDelivery(manager *githubdelivery.Manager) { a.githubPublisher = manager }
func (a *API) githubDeliveryRoutes(mux *http.ServeMux) {
	register := func(route string, h http.HandlerFunc) { mux.HandleFunc(route, a.authorizeRoute(route, h)) }
	register("GET /api/github-repositories", func(w http.ResponseWriter, r *http.Request) {
		items, err := a.store.GitHubRepositories(r.Context())
		a.respond(w, r, map[string]any{"repositories": items}, err)
	})
	register("POST /api/github-repositories", func(w http.ResponseWriter, r *http.Request) {
		var input orchestration.GitHubRepositoryInput
		if !decode(w, r, &input, 8<<10) {
			return
		}
		if a.githubPublisher == nil {
			writeError(w, 503, "execution_unavailable", "GitHub delivery is unavailable.")
			return
		}
		value, err := a.githubPublisher.Connect(r.Context(), input)
		a.respond(w, r, map[string]any{"repository": value}, err)
	})
	register("GET /api/github-repository-requests/{id}", func(w http.ResponseWriter, r *http.Request) {
		value, err := a.store.GitHubRepositoryRequest(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"repository": value}, err)
	})
	register("POST /api/github-repository-requests/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !decode(w, r, &in, 4096) {
			return
		}
		value, err := a.store.CancelGitHubRepositoryRequest(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"repository": value, "cancelled": value == nil}, err)
	})
	register("POST /api/work-items/{id}/github-delivery/check", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Revision int64 `json:"revision"`
		}
		if !decode(w, r, &input, 4096) {
			return
		}
		value, err := a.store.CheckGitHubDelivery(r.Context(), r.PathValue("id"), input.Revision)
		a.respond(w, r, map[string]any{"item": value}, err)
	})
}
