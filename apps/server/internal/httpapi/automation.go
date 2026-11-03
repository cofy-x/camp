package httpapi

import (
	"errors"
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (a *API) EnableAutomation(credentials *configuration.GitHubCredentials) {
	a.commentCredentials = credentials
}
func (a *API) automationRoutes(mux *http.ServeMux) {
	register := func(route string, h http.HandlerFunc) { mux.HandleFunc(route, a.authorizeRoute(route, h)) }
	register("GET /api/rooms/{id}/automation-policies", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.store.AutomationPolicies(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"policies": p}, e)
	})
	register("PUT /api/rooms/{id}/automation-policies/{source}", a.admin(func(w http.ResponseWriter, r *http.Request) {
		var in orchestration.PolicyInput
		if !decode(w, r, &in, 32<<10) {
			return
		}
		p, e := a.store.SaveAutomationPolicy(r.Context(), r.PathValue("id"), r.PathValue("source"), in)
		a.respond(w, r, map[string]any{"policy": p}, e)
	}))
	register("GET /api/automation-requests/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.AutomationRequest(r.Context(), r.PathValue("id"))
		a.respond(w, r, v, e)
	})
	register("POST /api/automation-requests/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !decode(w, r, &in, 4096) {
			return
		}
		v, e := a.store.CancelAutomationRequest(r.Context(), r.PathValue("id"))
		a.respond(w, r, v, e)
	})
	register("GET /api/automation-evaluations/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.GetAutomationEvaluation(r.Context(), r.PathValue("id"))
		a.respond(w, r, v, e)
	})
	register("GET /api/rooms/{id}/automation-events", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		direction := "asc"
		if values, present := query["direction"]; present {
			if len(values) != 1 || (values[0] != "asc" && values[0] != "desc") {
				writeError(w, 400, "invalid_page", "Choose one ascending or descending event order.")
				return
			}
			direction = values[0]
		}
		if len(query["sourceId"]) > 1 {
			writeError(w, 400, "invalid_page", "Choose one event source.")
			return
		}
		after, afterPresent, ok := pageNumber(w, r, "after")
		if !ok {
			return
		}
		before, beforePresent, ok := pageNumber(w, r, "before")
		if !ok {
			return
		}
		through, throughPresent, ok := pageNumber(w, r, "through")
		if !ok {
			return
		}
		limit, _, ok := pageNumber(w, r, "limit")
		if !ok {
			return
		}
		if limit > 20 || (direction == "desc" && afterPresent) || (direction == "asc" && beforePresent) {
			writeError(w, 400, "invalid_page", "Choose one event cursor and at most 20 events.")
			return
		}
		in := storage.AutomationEventQuery{Direction: direction, SourceID: query.Get("sourceId"), After: after, Limit: int(limit)}
		if throughPresent {
			in.Through = &through
		}
		if beforePresent {
			in.Before = &before
		}
		v, e := a.store.AutomationActivity(r.Context(), r.PathValue("id"), in)
		a.respond(w, r, v, e)
	})
	register("GET /api/tasks/{id}/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.TaskSubscriptions(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"subscriptions": v}, e)
	})
	register("PUT /api/tasks/{id}/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		var in orchestration.SubscriptionInput
		if !decode(w, r, &in, 4096) {
			return
		}
		v, e := a.store.SetTaskSubscription(r.Context(), r.PathValue("id"), in)
		a.respond(w, r, map[string]any{"subscription": v}, e)
	})
	register("GET /api/tasks/{id}/deliveries", func(w http.ResponseWriter, r *http.Request) {
		limit, _, ok := pageNumber(w, r, "limit")
		if !ok {
			return
		}
		if limit > 20 || len(r.URL.Query()["cursor"]) > 1 {
			writeError(w, 400, "invalid_page", "Choose one cursor and at most 20 comment deliveries.")
			return
		}
		v, e := a.store.TaskAutomationDeliveriesPage(r.Context(), r.PathValue("id"), r.URL.Query().Get("cursor"), int(limit))
		a.respond(w, r, v, e)
	})
	register("POST /api/automation-deliveries/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		var in storage.AutomationDeliveryInput
		if !decode(w, r, &in, 4096) {
			return
		}
		v, e := a.store.OperateAutomationDelivery(r.Context(), r.PathValue("id"), r.PathValue("action"), in)
		a.respond(w, r, map[string]any{"delivery": v}, e)
	})
	register("PUT /api/github-sources/{id}/comment-credential", a.admin(func(w http.ResponseWriter, r *http.Request) {
		var in storage.AutomationCredentialInput
		if !decode(w, r, &in, 4096) {
			return
		}
		if v, found, e := a.store.FindAutomationCredential(r.Context(), r.PathValue("id"), in); e != nil || found {
			a.respond(w, r, v, e)
			return
		}
		if a.commentCredentials == nil {
			writeError(w, 503, "settings_unavailable", "Comment credentials are not initialized.")
			return
		}
		ref, e := a.commentCredentials.Write(in.Token, in.ActorID)
		if e != nil {
			var p *configuration.Error
			if errors.As(e, &p) {
				writeError(w, 400, p.Code, p.Message)
			} else {
				a.fail(w, r, e)
			}
			return
		}
		v, e := a.store.SetAutomationCredential(r.Context(), r.PathValue("id"), in, ref)
		a.respond(w, r, v, e)
	}))
}
