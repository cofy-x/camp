package storage

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/feishuhooks"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func feishuSource(row sqlgen.FeishuSource) FeishuSource {
	return FeishuSource{ID: row.ID, Kind: "feishu", RoomID: row.RoomID, AppID: row.AppID, TenantKey: row.TenantKey, ChatID: row.ChatID, Enabled: row.Enabled != 0, Revision: row.Revision, CredentialRef: row.CredentialRef, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

// FeishuSourceCredential is only for the trusted ingress adapter. Every durable
// intake checks this exact immutable credential again in its transaction.
func (s *Store) FeishuSourceCredential(ctx context.Context, id string) (FeishuSource, error) {
	row, err := s.queries().GetFeishuSource(ctx, id)
	if err == nil && row.Enabled == 0 {
		return FeishuSource{}, problem("source_unavailable", "The Feishu source is disabled.")
	}
	return feishuSource(row), missing(err, "The Feishu source")
}
func (s *Store) ListFeishuSources(ctx context.Context, room string) ([]FeishuSource, error) {
	if err := requireActor(ctx, s.queries(), false); err != nil {
		return nil, err
	}
	if _, err := s.GetRoom(ctx, room); err != nil {
		return nil, err
	}
	rows, err := s.queries().ListFeishuSources(ctx, room)
	out := make([]FeishuSource, 0, len(rows))
	for _, row := range rows {
		out = append(out, feishuSource(row))
	}
	return out, err
}

func feishuReceipt(ctx context.Context, q *sqlgen.Queries, request string, input any) (FeishuReceipt, bool, string, error) {
	if !validText(request, 128, false) {
		return FeishuReceipt{}, false, "", problem("invalid_source", "A stable request ID is required.")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return FeishuReceipt{}, false, "", err
	}
	hash := remoteHash(raw)
	old, err := q.GetFeishuRequest(ctx, sqlgen.GetFeishuRequestParams{UserID: actorID(ctx), RequestID: request})
	if errors.Is(err, sql.ErrNoRows) {
		return FeishuReceipt{}, false, hash, nil
	}
	if err != nil {
		return FeishuReceipt{}, false, hash, err
	}
	var result FeishuReceipt
	if err = json.Unmarshal([]byte(old.Result), &result); err != nil {
		return result, false, hash, err
	}
	if result.Kind == "cancelled" {
		return result, false, hash, problem("request_cancelled", "This unconfirmed operation was cancelled. Keep its draft and use a new request ID.")
	}
	if old.RequestHash != hash {
		return FeishuReceipt{}, false, hash, problem("request_conflict", "This request ID belongs to a different Feishu operation.")
	}
	return result, true, hash, nil
}

// CancelFeishuRequest fences a delayed configuration write without requiring its
// secret-bearing request body to survive a browser reload.
func (s *Store) CancelFeishuRequest(ctx context.Context, request string) (out FeishuReceipt, err error) {
	if !validText(request, 128, false) {
		return out, problem("invalid_source", "A stable request ID is required.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := githubActor(ctx, q, false); e != nil {
			return e
		}
		old, e := q.GetFeishuRequest(ctx, sqlgen.GetFeishuRequestParams{UserID: actorID(ctx), RequestID: request})
		if e == nil {
			return json.Unmarshal([]byte(old.Result), &out)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		out.Kind = "cancelled"
		return saveFeishuReceipt(ctx, q, request, "cancelled", out)
	})
	return
}
func saveFeishuReceipt(ctx context.Context, q *sqlgen.Queries, request, hash string, result FeishuReceipt) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return q.SaveFeishuRequest(ctx, sqlgen.SaveFeishuRequestParams{UserID: actorID(ctx), RequestID: request, RequestHash: hash, Result: string(raw)})
}
func (s *Store) FeishuRequest(ctx context.Context, request string) (FeishuReceipt, error) {
	if err := githubActor(ctx, s.queries(), false); err != nil {
		return FeishuReceipt{}, err
	}
	row, err := s.queries().GetFeishuRequest(ctx, sqlgen.GetFeishuRequestParams{UserID: actorID(ctx), RequestID: request})
	if err != nil {
		return FeishuReceipt{}, missing(err, "The Feishu operation receipt")
	}
	var result FeishuReceipt
	err = json.Unmarshal([]byte(row.Result), &result)
	return result, err
}

func (s *Store) FindFeishuSourceCreation(ctx context.Context, room string, in CreateFeishuSourceInput) (FeishuSource, bool, error) {
	if err := githubActor(ctx, s.queries(), true); err != nil {
		return FeishuSource{}, false, err
	}
	old, found, _, err := feishuReceipt(ctx, s.queries(), in.RequestID, []any{"source.create", room, in})
	if old.Source != nil {
		return *old.Source, found, err
	}
	return FeishuSource{}, found, err
}
func (s *Store) FindFeishuSourceUpdate(ctx context.Context, id string, in UpdateFeishuSourceInput) (FeishuSource, bool, error) {
	if err := githubActor(ctx, s.queries(), true); err != nil {
		return FeishuSource{}, false, err
	}
	old, found, _, err := feishuReceipt(ctx, s.queries(), in.RequestID, []any{"source.update", id, in})
	if old.Source != nil {
		return *old.Source, found, err
	}
	return FeishuSource{}, found, err
}
func validFeishuCredentialRef(ref string) bool {
	if len(ref) != 48 {
		return false
	}
	_, err := hex.DecodeString(ref)
	return err == nil
}
func (s *Store) CreateFeishuSource(ctx context.Context, room string, in CreateFeishuSourceInput, credentialRef string) (out FeishuSource, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := githubActor(ctx, q, true); e != nil {
			return e
		}
		old, replay, hash, e := feishuReceipt(ctx, q, in.RequestID, []any{"source.create", room, in})
		if e != nil {
			return e
		}
		if replay {
			out = *old.Source
			return nil
		}
		if !feishuhooks.IdentityPattern.MatchString(in.AppID) || !feishuhooks.IdentityPattern.MatchString(in.TenantKey) || !feishuhooks.IdentityPattern.MatchString(in.ChatID) || !validFeishuCredentialRef(credentialRef) {
			return problem("invalid_source", "Provide the immutable Feishu app, tenant and chat IDs, and save its private credentials.")
		}
		if _, e = q.GetRoom(ctx, room); e != nil {
			return missing(e, "The Room")
		}
		rows, e := q.ListFeishuSources(ctx, room)
		if e != nil {
			return e
		}
		if len(rows) >= 32 {
			return problem("invalid_source", "A Room supports at most 32 Feishu sources.")
		}
		for _, row := range rows {
			if row.Enabled != 0 && row.AppID == in.AppID && row.TenantKey == in.TenantKey && row.ChatID == in.ChatID {
				return problem("source_conflict", "Disable the active source before creating its replacement.")
			}
		}
		id, stamp := newID(), now()
		if e = q.CreateFeishuSource(ctx, sqlgen.CreateFeishuSourceParams{ID: id, RoomID: room, AppID: in.AppID, TenantKey: in.TenantKey, ChatID: in.ChatID, CredentialRef: credentialRef, CreatedBy: actorID(ctx), CreatedAt: stamp, UpdatedAt: stamp}); e != nil {
			return e
		}
		row, e := q.GetFeishuSource(ctx, id)
		if e != nil {
			return e
		}
		out = feishuSource(row)
		if e = event(ctx, q, "feishu.source.updated", room, id); e != nil {
			return e
		}
		return saveFeishuReceipt(ctx, q, in.RequestID, hash, FeishuReceipt{Kind: "source.create", Source: &out})
	})
	return
}
func (s *Store) UpdateFeishuSource(ctx context.Context, id string, in UpdateFeishuSourceInput, credentialRef string) (out FeishuSource, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := githubActor(ctx, q, true); e != nil {
			return e
		}
		old, replay, hash, e := feishuReceipt(ctx, q, in.RequestID, []any{"source.update", id, in})
		if e != nil {
			return e
		}
		if replay {
			out = *old.Source
			return nil
		}
		row, e := q.GetFeishuSource(ctx, id)
		if e != nil {
			return missing(e, "The Feishu source")
		}
		if row.Revision != in.Revision {
			return problem("revision_conflict", "The source changed. Refresh before saving.")
		}
		rotate := in.VerificationToken != "" || in.EncryptKey != ""
		if in.Enabled == nil && !rotate {
			return problem("invalid_source", "Choose a source setting to change.")
		}
		if rotate {
			if in.VerificationToken == "" || in.EncryptKey == "" || !validFeishuCredentialRef(credentialRef) {
				return problem("invalid_source", "Rotate the Verification Token and Encrypt Key together.")
			}
			row.CredentialRef = credentialRef
		}
		if in.Enabled != nil {
			row.Enabled = 0
			if *in.Enabled {
				others, e := q.ListFeishuSources(ctx, row.RoomID)
				if e != nil {
					return e
				}
				for _, other := range others {
					if other.ID != id && other.Enabled != 0 && other.AppID == row.AppID && other.TenantKey == row.TenantKey && other.ChatID == row.ChatID {
						return problem("source_conflict", "Another source for this app, tenant and chat is enabled.")
					}
				}
				row.Enabled = 1
			}
		}
		if e = q.UpdateFeishuSource(ctx, sqlgen.UpdateFeishuSourceParams{ID: id, Enabled: row.Enabled, CredentialRef: row.CredentialRef, UpdatedAt: now()}); e != nil {
			return e
		}
		row, e = q.GetFeishuSource(ctx, id)
		if e != nil {
			return e
		}
		out = feishuSource(row)
		if e = event(ctx, q, "feishu.source.updated", out.RoomID, id); e != nil {
			return e
		}
		return saveFeishuReceipt(ctx, q, in.RequestID, hash, FeishuReceipt{Kind: "source.update", Source: &out})
	})
	return
}

func feishuEvent(ctx context.Context, q *sqlgen.Queries, id string) (FeishuEvent, error) {
	row, err := q.GetFeishuEvent(ctx, id)
	if err != nil {
		return FeishuEvent{}, missing(err, "The Feishu event")
	}
	var out FeishuEvent
	if err = json.Unmarshal([]byte(row.Value), &out.Observation); err != nil {
		return out, err
	}
	out.ID, out.Seq, out.SourceID, out.RoomID, out.SourceRevision, out.Fingerprint, out.ReceivedAt = row.ID, row.Seq, row.SourceID, row.RoomID, row.SourceRevision, row.Fingerprint, row.ReceivedAt
	return out, nil
}
func (s *Store) GetFeishuEvent(ctx context.Context, id string) (FeishuEvent, error) {
	if err := requireActor(ctx, s.queries(), false); err != nil {
		return FeishuEvent{}, err
	}
	return feishuEvent(ctx, s.queries(), id)
}
func (s *Store) FeishuPayload(ctx context.Context, id string) ([]byte, error) {
	if err := requireActor(ctx, s.queries(), false); err != nil {
		return nil, err
	}
	raw, err := s.queries().GetFeishuPayload(ctx, id)
	return raw, missing(err, "The Feishu event original")
}

// ReceiveFeishuDelivery accepts decrypted bytes only from the verified ingress.
// Parse and hash before acquiring SQLite; bind all scope and credential checks in
// the transaction that commits the immutable original, receipt and canonical event.
func (s *Store) ReceiveFeishuDelivery(ctx context.Context, id, credential string, payload, plain []byte) (out FeishuEvent, duplicate bool, err error) {
	if len(payload) > feishuhooks.MaxPayloadBytes {
		return out, false, problem("invalid_source", "The encrypted Feishu event exceeds 1 MiB.")
	}
	o, fingerprint, e := feishuhooks.Parse(plain)
	if e != nil {
		return out, false, problem("invalid_source", e.Error())
	}
	raw, e := json.Marshal(o)
	if e != nil {
		return out, false, e
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		src, e := q.GetFeishuSource(ctx, id)
		if e != nil {
			return missing(e, "The Feishu source")
		}
		if src.Enabled == 0 || src.CredentialRef != credential {
			return problem("source_unavailable", "This source is disabled or its credentials changed.")
		}
		if src.AppID != o.AppID || src.TenantKey != o.TenantKey || (o.Kind == "im.message.receive_v1" && src.ChatID != o.ChatID) {
			return problem("source_conflict", "The event is outside the configured Feishu app, tenant or chat.")
		}
		previous, e := q.GetFeishuDelivery(ctx, sqlgen.GetFeishuDeliveryParams{SourceID: id, DeliveryID: o.DeliveryID})
		if e == nil {
			if previous.Fingerprint != fingerprint {
				return problem("request_conflict", "This Feishu event ID belongs to different content.")
			}
			duplicate = true
			out, e = feishuEvent(ctx, q, previous.EventID)
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		eventID, e := q.GetFeishuFingerprint(ctx, sqlgen.GetFeishuFingerprintParams{SourceID: id, Fingerprint: fingerprint})
		if errors.Is(e, sql.ErrNoRows) {
			eventID = newID()
			if e = q.InsertFeishuEvent(ctx, sqlgen.InsertFeishuEventParams{ID: eventID, SourceID: id, RoomID: src.RoomID, SourceRevision: src.Revision, CredentialRef: credential, Fingerprint: fingerprint, Value: string(raw), Payload: payload, ReceivedAt: now()}); e != nil {
				return e
			}
			out, e = feishuEvent(ctx, q, eventID)
			if e != nil {
				return e
			}
			if e = recordAutomationFeishuEvent(ctx, tx, out); e != nil {
				return e
			}
			if e = event(ctx, q, "feishu.event.received", src.RoomID, eventID); e != nil {
				return e
			}
		} else if e != nil {
			return e
		} else {
			duplicate = true
		}
		if e = q.InsertFeishuDelivery(ctx, sqlgen.InsertFeishuDeliveryParams{SourceID: id, DeliveryID: o.DeliveryID, Fingerprint: fingerprint, EventID: eventID}); e != nil {
			return e
		}
		out, e = feishuEvent(ctx, q, eventID)
		return e
	})
	return
}

func recordAutomationFeishuEvent(ctx context.Context, tx *sql.Tx, e FeishuEvent) error {
	actorKind := "bot"
	if e.ActorKind == "user" {
		actorKind = "user"
	}
	v := orchestration.SourceEvent{ID: "feishu:" + e.ID, RoomID: e.RoomID, SourceID: e.SourceID, ThreadKey: e.ThreadKey(), Kind: e.Kind, ActorID: e.ActorID(), ActorLogin: e.ActorOpenID, ActorKind: actorKind, Content: e.Context(), OccurredAt: e.OccurredAt, Reason: e.IgnoreReason, Feishu: &orchestration.FeishuEventRef{EventID: e.ID, AppID: e.AppID, TenantKey: e.TenantKey, ChatID: e.ChatID, MessageID: e.MessageID, ThreadID: e.ThreadID, ActorOpenID: e.ActorOpenID}}
	return recordAutomationSourceEvent(ctx, tx, v, e.IgnoreReason == "")
}
