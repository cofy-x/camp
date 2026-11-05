package httpapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/githubhooks"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (a *API) EnableGitHub(secrets *configuration.WebhookSecrets) { a.webhookSecrets = secrets }

func (a *API) githubRoutes(mux *http.ServeMux) {
	if a.hooks == nil {
		a.hooks = http.NewServeMux()
	}
	a.hooks.HandleFunc("POST /api/hooks/github/{id}", a.githubDelivery)
	register := func(route string, h http.HandlerFunc) { mux.HandleFunc(route, a.authorizeRoute(route, h)) }
	register("GET /api/rooms/{id}/github-sources", func(w http.ResponseWriter, r *http.Request) {
		sources, err := a.store.ListGitHubSources(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"sources": sources}, err)
	})
	register("POST /api/rooms/{id}/github-sources", a.admin(func(w http.ResponseWriter, r *http.Request) {
		var in storage.CreateGitHubSourceInput
		if !decode(w, r, &in, 4096) {
			return
		}
		if original, found, err := a.store.FindGitHubSourceCreation(r.Context(), r.PathValue("id"), in); err != nil || found {
			a.respond(w, r, map[string]any{"source": original}, err)
			return
		}
		ref, ok := a.saveWebhookSecret(w, r, in.Secret)
		if !ok {
			return
		}
		source, err := a.store.CreateGitHubSource(r.Context(), r.PathValue("id"), in, ref)
		a.respond(w, r, map[string]any{"source": source}, err)
	}))
	register("PATCH /api/github-sources/{id}", a.admin(func(w http.ResponseWriter, r *http.Request) {
		var in storage.UpdateGitHubSourceInput
		if !decode(w, r, &in, 4096) {
			return
		}
		if original, found, err := a.store.FindGitHubSourceUpdate(r.Context(), r.PathValue("id"), in); err != nil || found {
			a.respond(w, r, map[string]any{"source": original}, err)
			return
		}
		ref := ""
		if in.Secret != "" {
			var ok bool
			ref, ok = a.saveWebhookSecret(w, r, in.Secret)
			if !ok {
				return
			}
		}
		source, err := a.store.UpdateGitHubSource(r.Context(), r.PathValue("id"), in, ref)
		a.respond(w, r, map[string]any{"source": source}, err)
	}))
	register("GET /api/github-requests/{id}", func(w http.ResponseWriter, r *http.Request) {
		receipt, err := a.store.GitHubRequest(r.Context(), r.PathValue("id"))
		a.respond(w, r, receipt, err)
	})
	register("GET /api/rooms/{id}/github-events", func(w http.ResponseWriter, r *http.Request) {
		after, _, ok := pageNumber(w, r, "after")
		if !ok {
			return
		}
		through, present, ok := pageNumber(w, r, "through")
		if !ok {
			return
		}
		limit, _, ok := pageNumber(w, r, "limit")
		if !ok {
			return
		}
		if limit > 20 || len(r.URL.Query()["state"]) > 1 {
			writeError(w, 400, "invalid_page", "Choose one event state and at most 20 events.")
			return
		}
		var end *int64
		if present {
			end = &through
		}
		page, err := a.store.GitHubEventsPage(r.Context(), r.PathValue("id"), r.URL.Query().Get("state"), after, end, int(limit))
		a.respond(w, r, page, err)
	})
	register("GET /api/github-events/{id}", func(w http.ResponseWriter, r *http.Request) {
		event, err := a.store.GetGitHubEvent(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"event": event}, err)
	})
	register("GET /api/github-events/{id}/payload", func(w http.ResponseWriter, r *http.Request) {
		raw, err := a.store.GitHubPayload(r.Context(), r.PathValue("id"))
		if err != nil {
			a.fail(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="github-event.json"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(200)
		_, _ = w.Write(raw)
	})
	register("POST /api/github-events/{id}/dismiss", func(w http.ResponseWriter, r *http.Request) {
		var in storage.DismissGitHubEventInput
		if !decode(w, r, &in, 4096) {
			return
		}
		event, err := a.store.DismissGitHubEvent(r.Context(), r.PathValue("id"), in)
		a.respond(w, r, map[string]any{"event": event}, err)
	})
}

func (a *API) saveWebhookSecret(w http.ResponseWriter, r *http.Request, secret string) (string, bool) {
	if a.webhookSecrets == nil {
		writeError(w, 503, "settings_unavailable", "Webhook credentials are not initialized.")
		return "", false
	}
	ref, err := a.webhookSecrets.Put(secret)
	var problem *configuration.Error
	if errors.As(err, &problem) {
		writeError(w, 400, problem.Code, problem.Message)
		return "", false
	}
	if err != nil {
		a.fail(w, r, err)
		return "", false
	}
	return ref, true
}

func (a *API) githubDelivery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.allowTransport(w, r) {
		return
	}
	for _, header := range []string{"Cookie", "Origin", "Authorization"} {
		if len(r.Header.Values(header)) != 0 {
			writeError(w, 403, "invalid_webhook", "Use signature authentication without browser credentials.")
			return
		}
	}
	if a.webhookSecrets == nil {
		writeError(w, 503, "settings_unavailable", "Webhook credentials are not initialized.")
		return
	}
	for _, header := range []string{"X-Hub-Signature-256", "X-GitHub-Delivery", "X-GitHub-Event", "Content-Type"} {
		if len(r.Header.Values(header)) != 1 {
			writeError(w, 400, "invalid_webhook", "Provide one signature, delivery GUID, event type and JSON content type.")
			return
		}
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, 415, "invalid_webhook", "Use application/json deliveries.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, githubhooks.MaxPayloadBytes))
	if err != nil {
		writeError(w, 413, "invalid_webhook", "The webhook payload must fit within 1 MiB.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	source, err := a.store.GitHubSourceCredential(ctx, r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	secret, err := a.webhookSecrets.Get(source.SecretRef)
	if err != nil {
		writeError(w, 503, "source_unavailable", "The source's private credential is unavailable.")
		return
	}
	if !githubhooks.Verify(secret, body, r.Header.Get("X-Hub-Signature-256")) {
		writeError(w, 401, "invalid_signature", "The delivery signature is invalid.")
		return
	}
	event, duplicate, err := a.store.ReceiveGitHubDelivery(ctx, source.ID, source.SecretRef, r.Header.Get("X-GitHub-Delivery"), r.Header.Get("X-GitHub-Event"), body)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// Only a committed observation is acknowledged. No provider or GitHub API is
	// called here, and no external text can select an Agent or enlarge a grant.
	a.write(w, 200, map[string]any{"eventId": event.ID, "status": event.Status, "duplicate": duplicate})
}
