package httpapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/feishuhooks"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (a *API) EnableFeishu(credentials *configuration.FeishuCredentials) {
	a.feishuCredentials = credentials
}

func (a *API) feishuRoutes(mux *http.ServeMux) {
	if a.hooks == nil {
		a.hooks = http.NewServeMux()
	}
	a.hooks.HandleFunc("POST /api/hooks/feishu/{id}", a.feishuDelivery)
	register := func(route string, h http.HandlerFunc) { mux.HandleFunc(route, a.authorizeRoute(route, h)) }
	register("GET /api/rooms/{id}/feishu-sources", func(w http.ResponseWriter, r *http.Request) {
		sources, err := a.store.ListFeishuSources(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"sources": sources}, err)
	})
	register("POST /api/rooms/{id}/feishu-sources", a.admin(func(w http.ResponseWriter, r *http.Request) {
		var in storage.CreateFeishuSourceInput
		if !decode(w, r, &in, 4096) {
			return
		}
		if original, found, err := a.store.FindFeishuSourceCreation(r.Context(), r.PathValue("id"), in); err != nil || found {
			a.respond(w, r, map[string]any{"source": original}, err)
			return
		}
		ref, ok := a.saveFeishuCredentials(w, r, in.VerificationToken, in.EncryptKey)
		if !ok {
			return
		}
		source, err := a.store.CreateFeishuSource(r.Context(), r.PathValue("id"), in, ref)
		a.respond(w, r, map[string]any{"source": source}, err)
	}))
	register("PATCH /api/feishu-sources/{id}", a.admin(func(w http.ResponseWriter, r *http.Request) {
		var in storage.UpdateFeishuSourceInput
		if !decode(w, r, &in, 4096) {
			return
		}
		if original, found, err := a.store.FindFeishuSourceUpdate(r.Context(), r.PathValue("id"), in); err != nil || found {
			a.respond(w, r, map[string]any{"source": original}, err)
			return
		}
		ref := ""
		if in.VerificationToken != "" || in.EncryptKey != "" {
			var ok bool
			ref, ok = a.saveFeishuCredentials(w, r, in.VerificationToken, in.EncryptKey)
			if !ok {
				return
			}
		}
		source, err := a.store.UpdateFeishuSource(r.Context(), r.PathValue("id"), in, ref)
		a.respond(w, r, map[string]any{"source": source}, err)
	}))
	register("GET /api/feishu-requests/{id}", func(w http.ResponseWriter, r *http.Request) {
		receipt, err := a.store.FeishuRequest(r.Context(), r.PathValue("id"))
		a.respond(w, r, receipt, err)
	})
	register("POST /api/feishu-requests/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		receipt, err := a.store.CancelFeishuRequest(r.Context(), r.PathValue("id"))
		a.respond(w, r, receipt, err)
	})
	register("GET /api/feishu-events/{id}", func(w http.ResponseWriter, r *http.Request) {
		event, err := a.store.GetFeishuEvent(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"event": event}, err)
	})
	register("GET /api/feishu-events/{id}/payload", func(w http.ResponseWriter, r *http.Request) {
		raw, err := a.store.FeishuPayload(r.Context(), r.PathValue("id"))
		if err != nil {
			a.fail(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="feishu-event.json"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	})
}

func (a *API) saveFeishuCredentials(w http.ResponseWriter, r *http.Request, token, key string) (string, bool) {
	if a.feishuCredentials == nil {
		writeError(w, 503, "settings_unavailable", "Feishu webhook credentials are not initialized.")
		return "", false
	}
	ref, err := a.feishuCredentials.Write(token, key)
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

func (a *API) feishuDelivery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.allowTransport(w, r) {
		return
	}
	for _, header := range []string{"Cookie", "Origin", "Authorization"} {
		if len(r.Header.Values(header)) != 0 {
			writeError(w, 403, "invalid_webhook", "Use Feishu webhook authentication without browser credentials.")
			return
		}
	}
	if a.feishuCredentials == nil {
		writeError(w, 503, "settings_unavailable", "Feishu webhook credentials are not initialized.")
		return
	}
	if len(r.Header.Values("Content-Type")) != 1 {
		writeError(w, 400, "invalid_webhook", "Provide one JSON content type.")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, 415, "invalid_webhook", "Use application/json deliveries.")
		return
	}
	for _, header := range []string{"X-Lark-Request-Timestamp", "X-Lark-Request-Nonce", "X-Lark-Signature"} {
		if len(r.Header.Values(header)) > 1 {
			writeError(w, 400, "invalid_webhook", "Provide at most one Feishu signature header of each kind.")
			return
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, feishuhooks.MaxPayloadBytes))
	if err != nil {
		writeError(w, 413, "invalid_webhook", "The encrypted webhook payload must fit within 1 MiB.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	source, err := a.store.FeishuSourceCredential(ctx, r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	credential, err := a.feishuCredentials.Read(source.CredentialRef)
	if err != nil {
		writeError(w, 503, "source_unavailable", "The source's private credential is unavailable.")
		return
	}
	// An encrypted challenge can omit signature headers, as in Feishu's URL
	// verification protocol. Ordinary events always require a valid signature.
	hasSignature := len(r.Header.Values("X-Lark-Signature")) != 0 || len(r.Header.Values("X-Lark-Request-Timestamp")) != 0 || len(r.Header.Values("X-Lark-Request-Nonce")) != 0
	verified := feishuhooks.Verify(credential.EncryptKey, body, r.Header.Get("X-Lark-Request-Timestamp"), r.Header.Get("X-Lark-Request-Nonce"), r.Header.Get("X-Lark-Signature"))
	if hasSignature && !verified {
		writeError(w, 401, "invalid_signature", "The Feishu delivery signature is invalid.")
		return
	}
	plain, err := feishuhooks.Decrypt(credential.EncryptKey, body)
	if err != nil {
		writeError(w, 401, "invalid_signature", "The Feishu delivery could not be authenticated.")
		return
	}
	challenge, err := feishuhooks.Authenticate(plain, credential.VerificationToken)
	if err != nil {
		writeError(w, 401, "invalid_signature", "The Feishu delivery could not be authenticated.")
		return
	}
	if challenge != "" {
		current, err := a.store.FeishuSourceCredential(ctx, source.ID)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		if current.CredentialRef != source.CredentialRef {
			writeError(w, 409, "source_unavailable", "The source credential changed.")
			return
		}
		a.write(w, 200, map[string]any{"challenge": challenge})
		return
	}
	if !verified {
		writeError(w, 401, "invalid_signature", "Ordinary Feishu events require signature authentication.")
		return
	}
	event, duplicate, err := a.store.ReceiveFeishuDelivery(ctx, source.ID, source.CredentialRef, body, plain)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.write(w, 200, map[string]any{"code": 0, "eventId": event.ID, "duplicate": duplicate})
}
