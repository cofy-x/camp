package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const MaxMemoryContentBytes = 8192
const MaxMemoryQueryBytes = 512
const MaxMemoryResults = 20
const memoryResultBytes = 64 << 10

func memoryActor(ctx context.Context, q *sqlgen.Queries) (User, error) {
	if err := requireActor(ctx, q, false); err != nil {
		return User{}, err
	}
	u, ok := UserFromContext(ctx)
	if !ok {
		return User{}, problem("unauthenticated", "An enabled team member is required.")
	}
	row, err := q.GetUser(ctx, u.ID)
	if err != nil {
		return User{}, err
	}
	return userRow(row), nil
}

func validateMemoryProvenance(ctx context.Context, q *sqlgen.Queries, room string, p orchestration.MemoryProvenance) error {
	empty := p
	empty.Kind = ""
	switch p.Kind {
	case "human_note":
		if empty != (orchestration.MemoryProvenance{}) {
			return problem("invalid_memory", "A human-authored note does not invent a source reference.")
		}
	case "message":
		empty.MessageID = ""
		if !validText(p.MessageID, 128, false) || empty != (orchestration.MemoryProvenance{}) {
			return problem("invalid_memory", "Message provenance identifies exactly one message.")
		}
		message, err := q.GetMessage(ctx, p.MessageID)
		if errors.Is(err, sql.ErrNoRows) || err == nil && message.RoomID != room {
			return problem("invalid_memory", "The source message must belong to this Room.")
		}
		return err
	case "source_event":
		empty.SourceEventID = ""
		if !validText(p.SourceEventID, 256, false) || empty != (orchestration.MemoryProvenance{}) {
			return problem("invalid_memory", "Event provenance identifies exactly one source event.")
		}
		event, err := q.GetAutomationEvent(ctx, p.SourceEventID)
		if errors.Is(err, sql.ErrNoRows) || err == nil && event.RoomID != room {
			return problem("invalid_memory", "The source event must belong to this Room.")
		}
		return err
	case "file":
		empty.ResourceID, empty.Commit, empty.Path = "", "", ""
		if empty != (orchestration.MemoryProvenance{}) {
			return problem("invalid_memory", "File provenance identifies only an exact resource, revision and path.")
		}
		ref := ReferenceInput{Kind: "file", ResourceID: p.ResourceID, Commit: p.Commit, Path: p.Path}
		if err := validateReferenceInput(ref); err != nil {
			return err
		}
		if _, err := validateResourceRevision(ctx, q, p.ResourceID, p.Commit); err != nil {
			return err
		}
		belongs, err := q.RoomHasResource(ctx, sqlgen.RoomHasResourceParams{RoomID: room, ResourceID: p.ResourceID})
		if err != nil {
			return err
		}
		if !belongs.Valid || !belongs.Bool {
			return problem("invalid_memory", "The referenced resource must belong to this Room.")
		}
	default:
		return problem("invalid_memory", "Choose human_note, message, source_event or file provenance.")
	}
	return nil
}

func getMemory(ctx context.Context, q *sqlgen.Queries, id string, revision int64) (orchestration.Memory, error) {
	head, err := q.GetMemoryHead(ctx, id)
	if err != nil {
		return orchestration.Memory{}, missing(err, "The memory")
	}
	if revision == 0 {
		revision = head.Revision
	}
	row, err := q.GetMemoryRevision(ctx, sqlgen.GetMemoryRevisionParams{MemoryID: id, Revision: revision})
	if err != nil {
		return orchestration.Memory{}, missing(err, "The memory revision")
	}
	var out orchestration.Memory
	if err = json.Unmarshal([]byte(row.Value), &out); err != nil {
		return out, err
	}
	out.Current = revision == head.Revision
	return out, nil
}

// GetMemory reads the current revision, or an explicit immutable historical one.
// Retracted content is available only through this explicit identity lookup.
func (s *Store) GetMemory(ctx context.Context, id string, revision int64) (out orchestration.Memory, err error) {
	if revision < 0 {
		return out, problem("invalid_memory", "Use a positive revision or omit it for the current revision.")
	}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if _, e := memoryActor(ctx, q); e != nil {
			return e
		}
		var e error
		out, e = getMemory(ctx, q, id, revision)
		return e
	})
	return
}

// SnapshotMemories returns whole current active revisions for a frozen decision.
// Callers without a public actor must establish their existing policy authority
// in this same transaction. Returned text remains untrusted evidence.
func SnapshotMemories(ctx context.Context, q *sqlgen.Queries, room, query string, limit int) ([]orchestration.Memory, error) {
	if !validText(query, MaxMemoryQueryBytes, true) || limit < 0 || limit > MaxMemoryResults {
		return nil, problem("invalid_memory", "Use at most 512 query bytes and 20 memory results.")
	}
	if err := requireActor(ctx, q, false); err != nil {
		return nil, err
	}
	if _, err := q.GetRoom(ctx, room); err != nil {
		return nil, missing(err, "The Room")
	}
	if limit == 0 {
		limit = 10
	}
	rows, err := q.SearchMemories(ctx, sqlgen.SearchMemoriesParams{RoomID: room, SearchText: strings.TrimSpace(query), ResultLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := []orchestration.Memory{}
	for _, raw := range rows {
		var memory orchestration.Memory
		if err = json.Unmarshal([]byte(raw), &memory); err != nil {
			return nil, err
		}
		memory.Current = true
		out = append(out, memory)
		encoded, err := json.Marshal(map[string]any{"memories": out})
		if err != nil {
			return nil, err
		}
		if len(encoded)+1 > memoryResultBytes {
			out = out[:len(out)-1]
			break
		}
	}
	return out, nil
}

func (s *Store) Memories(ctx context.Context, room, query string, limit int) (out []orchestration.Memory, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if _, e := memoryActor(ctx, q); e != nil {
			return e
		}
		var e error
		out, e = SnapshotMemories(ctx, q, room, query, limit)
		return e
	})
	return
}

func memoryWriteIntent(room, id, action string, in any) any {
	return struct {
		RoomID string `json:"roomId"`
		ID     string `json:"id"`
		Action string `json:"action"`
		Input  any    `json:"input"`
	}{room, id, action, in}
}

func memoryRequest(ctx context.Context, q *sqlgen.Queries, requestID string, in any) (orchestration.MemoryReceipt, bool, string, error) {
	if !validText(requestID, 128, false) {
		return orchestration.MemoryReceipt{}, false, "", problem("invalid_memory", "A stable request ID is required.")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return orchestration.MemoryReceipt{}, false, "", err
	}
	hash := remoteHash(raw)
	row, err := q.GetMemoryRequest(ctx, sqlgen.GetMemoryRequestParams{UserID: actorID(ctx), RequestID: requestID})
	if errors.Is(err, sql.ErrNoRows) {
		return orchestration.MemoryReceipt{}, false, hash, nil
	}
	if err != nil {
		return orchestration.MemoryReceipt{}, false, hash, err
	}
	var out orchestration.MemoryReceipt
	if err = json.Unmarshal([]byte(row.Result), &out); err != nil {
		return out, false, hash, err
	}
	if out.Kind == "cancelled" {
		return out, false, hash, problem("request_cancelled", "This unconfirmed operation was cancelled. Use a new request ID to save it.")
	}
	if row.RequestHash != hash {
		return out, false, hash, problem("request_conflict", "This request ID belongs to another memory operation.")
	}
	return out, true, hash, nil
}

func saveMemoryRequest(ctx context.Context, q *sqlgen.Queries, id, hash string, receipt orchestration.MemoryReceipt) error {
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	return q.InsertMemoryRequest(ctx, sqlgen.InsertMemoryRequestParams{UserID: actorID(ctx), RequestID: id, RequestHash: hash, Result: string(raw)})
}

// FindSaveMemory resolves an identical replay before immutable-file preflight.
func (s *Store) FindSaveMemory(ctx context.Context, room, id string, in orchestration.MemoryInput) (out orchestration.Memory, found bool, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if _, e := memoryActor(ctx, q); e != nil {
			return e
		}
		r, ok, _, e := memoryRequest(ctx, q, in.RequestID, memoryWriteIntent(room, id, "save", in))
		if ok && r.Memory != nil {
			out, found = *r.Memory, true
		}
		return e
	})
	return
}

func insertMemoryRevision(ctx context.Context, q *sqlgen.Queries, m orchestration.Memory) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return q.InsertMemoryRevision(ctx, sqlgen.InsertMemoryRevisionParams{MemoryID: m.ID, Revision: m.Revision, State: m.State, Content: m.Content, Value: string(raw), CreatedAt: m.CreatedAt})
}

func memoryManage(actor User, memory orchestration.Memory) error {
	if actor.Role != "admin" && actor.ID != memory.AuthorUserID {
		return problem("forbidden", "Only the memory author or an administrator can change it.")
	}
	return nil
}

// SaveMemory creates or supersedes a revision. It grants no work or model call.
func (s *Store) SaveMemory(ctx context.Context, room, id string, in orchestration.MemoryInput) (out orchestration.Memory, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		actor, e := memoryActor(ctx, q)
		if e != nil {
			return e
		}
		r, found, hash, e := memoryRequest(ctx, q, in.RequestID, memoryWriteIntent(room, id, "save", in))
		if e != nil {
			return e
		}
		if found {
			out = *r.Memory
			return nil
		}
		if !validText(in.Content, MaxMemoryContentBytes, false) {
			return problem("invalid_memory", "Memory content must contain at most 8192 UTF-8 bytes.")
		}
		stamp := now()
		if id == "" {
			if in.Revision != 0 {
				return problem("revision_conflict", "A new memory starts at revision zero.")
			}
			if _, e = q.GetRoom(ctx, room); e != nil {
				return missing(e, "The Room")
			}
			out = orchestration.Memory{ID: newID(), RoomID: room, Revision: 1, State: "active", AuthorUserID: actor.ID}
		} else {
			previous, e := getMemory(ctx, q, id, 0)
			if e != nil {
				return e
			}
			if e = memoryManage(actor, previous); e != nil {
				return e
			}
			if previous.Revision != in.Revision || previous.State == "retracted" {
				return problem("revision_conflict", "The memory changed or was retracted. Reload before editing.")
			}
			if room != "" && room != previous.RoomID {
				return problem("invalid_memory", "A memory cannot move between Rooms.")
			}
			out = previous
			out.Revision++
			out.SupersedesRevision = previous.Revision
		}
		if e = validateMemoryProvenance(ctx, q, out.RoomID, in.Provenance); e != nil {
			return e
		}
		out.Content, out.Provenance, out.RecordedByUserID, out.CreatedAt, out.Current = in.Content, in.Provenance, actor.ID, stamp, true
		if id == "" {
			if e = q.InsertMemory(ctx, sqlgen.InsertMemoryParams{ID: out.ID, RoomID: out.RoomID, AuthorUserID: out.AuthorUserID, CreatedAt: stamp}); e != nil {
				return e
			}
		} else {
			n, e := q.AdvanceMemory(ctx, sqlgen.AdvanceMemoryParams{ID: id, Revision: in.Revision})
			if e != nil {
				return e
			}
			if n != 1 {
				return problem("revision_conflict", "The memory changed before this write.")
			}
		}
		if e = insertMemoryRevision(ctx, q, out); e != nil {
			return e
		}
		if e = saveMemoryRequest(ctx, q, in.RequestID, hash, orchestration.MemoryReceipt{Kind: "saved", Memory: &out}); e != nil {
			return e
		}
		return event(ctx, q, "memory.updated", out.RoomID, out.ID)
	})
	return
}

func (s *Store) RetractMemory(ctx context.Context, id string, in orchestration.MemoryRetraction) (out orchestration.Memory, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		actor, e := memoryActor(ctx, q)
		if e != nil {
			return e
		}
		r, found, hash, e := memoryRequest(ctx, q, in.RequestID, memoryWriteIntent("", id, "retract", in))
		if e != nil {
			return e
		}
		if found {
			out = *r.Memory
			return nil
		}
		out, e = getMemory(ctx, q, id, 0)
		if e != nil {
			return e
		}
		if e = memoryManage(actor, out); e != nil {
			return e
		}
		if out.Revision != in.Revision || out.State != "active" {
			return problem("revision_conflict", "The memory changed or was retracted.")
		}
		n, e := q.AdvanceMemory(ctx, sqlgen.AdvanceMemoryParams{ID: id, Revision: in.Revision})
		if e != nil {
			return e
		}
		if n != 1 {
			return problem("revision_conflict", "The memory changed before this retraction.")
		}
		out.SupersedesRevision = out.Revision
		out.Revision++
		out.State, out.RecordedByUserID, out.CreatedAt, out.Current = "retracted", actor.ID, now(), true
		if e = insertMemoryRevision(ctx, q, out); e != nil {
			return e
		}
		if e = saveMemoryRequest(ctx, q, in.RequestID, hash, orchestration.MemoryReceipt{Kind: "retracted", Memory: &out}); e != nil {
			return e
		}
		return event(ctx, q, "memory.updated", out.RoomID, out.ID)
	})
	return
}

func (s *Store) MemoryRequest(ctx context.Context, id string) (out orchestration.MemoryReceipt, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if _, e := memoryActor(ctx, q); e != nil {
			return e
		}
		row, e := q.GetMemoryRequest(ctx, sqlgen.GetMemoryRequestParams{UserID: actorID(ctx), RequestID: id})
		if e != nil {
			return missing(e, "The memory request")
		}
		return json.Unmarshal([]byte(row.Result), &out)
	})
	return
}

func (s *Store) CancelMemoryRequest(ctx context.Context, id string) (out orchestration.MemoryReceipt, err error) {
	if !validText(id, 128, false) {
		return out, problem("invalid_memory", "A stable request ID is required.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if _, e := memoryActor(ctx, q); e != nil {
			return e
		}
		row, e := q.GetMemoryRequest(ctx, sqlgen.GetMemoryRequestParams{UserID: actorID(ctx), RequestID: id})
		if e == nil {
			return json.Unmarshal([]byte(row.Result), &out)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		out.Kind = "cancelled"
		return saveMemoryRequest(ctx, q, id, "cancelled", out)
	})
	return
}
