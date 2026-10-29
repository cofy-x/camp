package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/avatars"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

var handlePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`)

func validText(s string, limit int, empty bool) bool {
	return utf8.ValidString(s) && len(s) <= limit && (empty || strings.TrimSpace(s) != "") && !strings.ContainsRune(s, 0)
}
func objectJSON(v json.RawMessage) bool {
	var obj map[string]json.RawMessage
	return len(v) <= 32768 && json.Unmarshal(v, &obj) == nil
}
func commitValid(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func missing(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return problem("not_found", what+" does not exist.")
	}
	return err
}
func event(ctx context.Context, q *sqlgen.Queries, kind, room, id string) error {
	sequence, err := q.CreateWorkspaceEvent(ctx, sqlgen.CreateWorkspaceEventParams{Type: kind, RoomID: room, EntityID: id, CreatedAt: now()})
	if err != nil {
		return err
	}
	if room != "" {
		return q.TouchRoom(ctx, sqlgen.TouchRoomParams{ActivitySeq: sequence, UpdatedAt: now(), ID: room})
	}
	return nil
}
func agentRow(a sqlgen.Agent) Agent {
	return Agent{OwnerUserID: a.OwnerUserID.String, PreferredRunnerID: a.PreferredRunnerID.String, ID: a.ID, Name: a.Name, Handle: a.Handle, Icon: a.Icon, Avatar: a.Avatar, Instructions: a.Instructions, Access: a.Access, Enabled: a.Enabled, Revision: a.Revision, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
}
func roomRow(r sqlgen.Room) Room {
	return Room{RunnerID: r.RunnerID.String, RunnerRevision: r.RunnerRevision, LastMessageSeq: r.LastMessageSeq, ID: r.ID, Name: r.Name, Description: r.Description, ResourceID: r.ResourceID, DefaultAgentID: r.DefaultAgentID.String, ActivitySeq: r.ActivitySeq, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}
func resourceRow(r sqlgen.Resource) Resource {
	return Resource{ID: r.ID, Name: r.Name, SourcePath: r.SourcePath, CurrentCommit: r.CurrentCommit, Environment: json.RawMessage(r.Environment), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}
func (s *Store) GetAgent(ctx context.Context, id string) (Agent, error) {
	a, e := s.queries().GetAgent(ctx, id)
	return agentRow(a), missing(e, "The agent")
}
func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, e := s.queries().ListAgents(ctx)
	out := make([]Agent, 0, len(rows))
	for _, r := range rows {
		out = append(out, agentRow(r))
	}
	return out, e
}
func (s *Store) SaveAgent(ctx context.Context, a Agent) (Agent, error) {
	var saved Agent
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		saved, err = saveAgent(ctx, sqlgen.New(tx), a)
		return err
	})
	return saved, err
}

// CreateRoomAgent makes a new identity and its first membership one committed
// fact. Creating a member never changes routing or submits work.
func (s *Store) CreateRoomAgent(ctx context.Context, room string, a Agent) (Agent, error) {
	var saved Agent
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if _, err := q.GetRoom(ctx, room); err != nil {
			return missing(err, "The room")
		}
		if a.ID != "" {
			return problem("invalid_agent", "Join an existing agent instead of creating it again.")
		}
		var err error
		saved, err = saveAgent(ctx, q, a)
		if err != nil {
			return err
		}
		return joinRoom(ctx, q, room, saved.ID)
	})
	return saved, err
}

func saveAgent(ctx context.Context, q *sqlgen.Queries, a Agent) (Agent, error) {
	if err := requireActor(ctx, q, false); err != nil {
		return Agent{}, err
	}
	previousRunner := ""
	if a.ID == "" {
		a.OwnerUserID = actorID(ctx)
		if a.PreferredRunnerID == "" && a.OwnerUserID != "" {
			defaults, err := executionDefaults(ctx, q, a.OwnerUserID)
			if err != nil {
				return Agent{}, err
			}
			a.PreferredRunnerID = defaults.PersonalRunnerID
		}
	} else {
		old, err := q.GetAgent(ctx, a.ID)
		if err != nil {
			return Agent{}, missing(err, "The agent")
		}
		if err = requireObjectOwner(ctx, q, old.OwnerUserID.String); err != nil {
			return Agent{}, err
		}
		a.OwnerUserID = old.OwnerUserID.String
		previousRunner = old.PreferredRunnerID.String
		if a.Avatar == "" {
			a.Avatar = old.Avatar
		}
	}
	if a.PreferredRunnerID != "" && a.PreferredRunnerID != previousRunner {
		runner, err := availableRunner(ctx, q, a.PreferredRunnerID)
		if err != nil {
			return Agent{}, err
		}
		if err = runnerAllowsAgent(ctx, q, runner, a); err != nil {
			return Agent{}, err
		}
	}
	a.Name = strings.TrimSpace(a.Name)
	a.Handle = strings.ToLower(strings.TrimSpace(a.Handle))
	if a.Icon == "" {
		a.Icon = "agent"
	}
	if a.Avatar == "" {
		var err error
		a.Avatar, err = chooseAgentAvatar(ctx, q)
		if err != nil {
			return Agent{}, err
		}
	}
	if !avatars.Valid(a.Avatar, avatars.Agent) {
		return Agent{}, problem("invalid_avatar", "Choose an Agent avatar from the catalog.")
	}
	if !validText(a.Name, 120, false) || !handlePattern.MatchString(a.Handle) || !validText(a.Instructions, 8192, true) || !validText(a.Icon, 48, false) || (a.Access != "read" && a.Access != "write") {
		return Agent{}, problem("invalid_agent", "Provide a name, a valid handle, instructions, and read or write access.")
	}
	if other, e := q.GetAgentByHandle(ctx, a.Handle); e == nil && other.ID != a.ID {
		return Agent{}, problem("name_conflict", "An agent already uses this handle.")
	} else if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return Agent{}, e
	}
	if _, err := q.GetUserByUsername(ctx, a.Handle); err == nil {
		return Agent{}, problem("name_conflict", "This handle belongs to a person.")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Agent{}, err
	}
	a.UpdatedAt = now()
	if a.ID == "" {
		a.ID = newID()
		a.CreatedAt = a.UpdatedAt
		a.Revision = 1
		if e := q.CreateAgent(ctx, sqlgen.CreateAgentParams{ID: a.ID, Name: a.Name, Handle: a.Handle, Icon: a.Icon, Avatar: a.Avatar, Instructions: a.Instructions, Access: a.Access, Enabled: a.Enabled, Revision: 1, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}); e != nil {
			return Agent{}, e
		}
	} else {
		old, e := q.GetAgent(ctx, a.ID)
		if e != nil {
			return Agent{}, missing(e, "The agent")
		}
		a.CreatedAt = old.CreatedAt
		n, e := q.UpdateAgent(ctx, sqlgen.UpdateAgentParams{ID: a.ID, Name: a.Name, Handle: a.Handle, Icon: a.Icon, Avatar: a.Avatar, Instructions: a.Instructions, Access: a.Access, Enabled: a.Enabled, Revision: a.Revision, UpdatedAt: a.UpdatedAt})
		if e != nil {
			return Agent{}, e
		}
		if n != 1 {
			return Agent{}, problem("revision_conflict", "The agent changed. Refresh before saving.")
		}
		a.Revision++
	}
	if err := q.SetAgentOwnership(ctx, sqlgen.SetAgentOwnershipParams{ID: a.ID, OwnerUserID: nullableParent(a.OwnerUserID), PreferredRunnerID: nullableParent(a.PreferredRunnerID)}); err != nil {
		return Agent{}, err
	}
	if !a.Enabled || a.Access != "write" {
		if err := revokeRuns(ctx, q, "The Agent was disabled or its write access was removed.", func(run Run) bool {
			return run.AgentID == a.ID && (!a.Enabled || run.AgentSnapshot.Access == "write")
		}); err != nil {
			return Agent{}, err
		}
	}
	return a, event(ctx, q, "agent.updated", "", a.ID)
}
func (s *Store) GetResource(ctx context.Context, id string) (Resource, error) {
	r, e := s.queries().GetResource(ctx, id)
	return resourceRow(r), missing(e, "The resource")
}
func (s *Store) ListResources(ctx context.Context) ([]Resource, error) {
	rows, e := s.queries().ListResources(ctx)
	out := make([]Resource, 0, len(rows))
	for _, r := range rows {
		out = append(out, resourceRow(r))
	}
	return out, e
}
func (s *Store) CreateResource(ctx context.Context, r Resource) (Resource, error) {
	if r.ID == "" {
		r.ID = newID()
	}
	if len(r.Environment) == 0 {
		r.Environment = json.RawMessage(`{}`)
	}
	if !validText(r.Name, 240, false) || !validText(r.SourcePath, 4096, true) || !commitValid(r.CurrentCommit) || !objectJSON(r.Environment) {
		return Resource{}, problem("invalid_resource", "The resource metadata is invalid.")
	}
	r.CreatedAt = now()
	r.UpdatedAt = r.CreatedAt
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := createResource(ctx, q, r); e != nil {
			return e
		}
		return event(ctx, q, "resource.updated", "", r.ID)
	})
	return r, e
}
func createResource(ctx context.Context, q *sqlgen.Queries, r Resource) error {
	if err := requireActor(ctx, q, r.SourcePath != ""); err != nil {
		return err
	}
	return q.CreateResource(ctx, sqlgen.CreateResourceParams{ID: r.ID, Name: r.Name, SourcePath: r.SourcePath, CurrentCommit: r.CurrentCommit, Environment: string(r.Environment), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
}
func (s *Store) UpdateResource(ctx context.Context, r Resource) (Resource, error) {
	if !validText(r.Name, 240, false) || !objectJSON(r.Environment) {
		return Resource{}, problem("invalid_resource", "The resource metadata is invalid.")
	}
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, true); err != nil {
			return err
		}
		old, e := q.GetResource(ctx, r.ID)
		if e != nil {
			return missing(e, "The resource")
		}
		if e := requireWorkingResource(ctx, q, r.ID); e != nil {
			return e
		}
		r.SourcePath = old.SourcePath
		r.CurrentCommit = old.CurrentCommit
		r.CreatedAt = old.CreatedAt
		r.UpdatedAt = now()
		if e = q.UpdateResource(ctx, sqlgen.UpdateResourceParams{ID: r.ID, Name: r.Name, Environment: string(r.Environment), UpdatedAt: r.UpdatedAt}); e != nil {
			return e
		}
		return event(ctx, q, "resource.updated", "", r.ID)
	})
	return r, e
}
func (s *Store) GetRoom(ctx context.Context, id string) (Room, error) {
	r, e := s.queries().GetRoom(ctx, id)
	return roomRow(r), missing(e, "The room")
}
func (s *Store) ListRooms(ctx context.Context) ([]Room, error) {
	rows, e := s.queries().ListRooms(ctx)
	out := make([]Room, 0, len(rows))
	for _, r := range rows {
		out = append(out, roomRow(r))
	}
	return out, e
}
func validateRoom(ctx context.Context, q *sqlgen.Queries, r *Room, creating bool) error {
	if err := requireActor(ctx, q, false); err != nil {
		return err
	}
	if strings.ContainsFunc(r.Name, unicode.IsControl) {
		return problem("invalid_room", "Use a room name without control characters.")
	}
	r.Name = strings.TrimSpace(r.Name)
	if !validText(r.Name, 120, false) {
		return problem("invalid_room", "Use a room name of 1–120 UTF-8 bytes.")
	}
	if !validText(r.Description, 4096, true) {
		return problem("invalid_room", "Use a room description of at most 4096 UTF-8 bytes without null characters.")
	}
	if _, e := q.GetResource(ctx, r.ResourceID); e != nil {
		return missing(e, "The resource")
	}
	if e := requireWorkingResource(ctx, q, r.ResourceID); e != nil {
		return e
	}
	if r.DefaultAgentID != "" {
		a, e := q.GetAgent(ctx, r.DefaultAgentID)
		if e != nil {
			return missing(e, "The agent")
		}
		if !a.Enabled {
			return problem("invalid_agent", "The default agent is disabled.")
		}
		if !creating {
			if _, e = q.GetMembership(ctx, sqlgen.GetMembershipParams{RoomID: r.ID, AgentID: a.ID}); e != nil {
				return problem("invalid_agent", "The default agent must join this room first.")
			}
		}
	}
	rooms, e := q.ListRooms(ctx)
	if e != nil {
		return e
	}
	for _, other := range rooms {
		if other.ID != r.ID && strings.EqualFold(other.Name, r.Name) {
			return problem("name_conflict", "A room already uses this name.")
		}
	}
	return nil
}
func (s *Store) CreateRoom(ctx context.Context, r Room) (Room, error) {
	if r.ID == "" {
		r.ID = newID()
	}
	r.CreatedAt = now()
	r.UpdatedAt = r.CreatedAt
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := validateRoom(ctx, q, &r, true); e != nil {
			return e
		}
		if e := q.CreateRoom(ctx, sqlgen.CreateRoomParams{ID: r.ID, Name: r.Name, Description: r.Description, ResourceID: r.ResourceID, DefaultAgentID: nullableParent(r.DefaultAgentID), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}); e != nil {
			return e
		}
		if r.DefaultAgentID != "" {
			if e := joinRoom(ctx, q, r.ID, r.DefaultAgentID); e != nil {
				return e
			}
		}
		return event(ctx, q, "room.updated", r.ID, r.ID)
	})
	return r, e
}
func (s *Store) UpdateRoom(ctx context.Context, r Room) (Room, error) {
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		old, e := q.GetRoom(ctx, r.ID)
		if e != nil {
			return missing(e, "The room")
		}
		r.CreatedAt = old.CreatedAt
		r.ActivitySeq = old.ActivitySeq
		r.RunnerID, r.RunnerRevision = old.RunnerID.String, old.RunnerRevision
		r.UpdatedAt = now()
		if e := validateRoom(ctx, q, &r, false); e != nil {
			return e
		}
		if e := q.UpdateRoom(ctx, sqlgen.UpdateRoomParams{ID: r.ID, Name: r.Name, Description: r.Description, ResourceID: r.ResourceID, DefaultAgentID: nullableParent(r.DefaultAgentID), UpdatedAt: r.UpdatedAt}); e != nil {
			return e
		}
		return event(ctx, q, "room.updated", r.ID, r.ID)
	})
	return r, e
}
func (s *Store) ListMembers(ctx context.Context, room string) ([]Agent, error) {
	if _, e := s.GetRoom(ctx, room); e != nil {
		return nil, e
	}
	rows, e := s.queries().ListMembers(ctx, room)
	out := make([]Agent, 0, len(rows))
	for _, r := range rows {
		out = append(out, agentRow(r))
	}
	return out, e
}
func (s *Store) JoinRoom(ctx context.Context, room, agent string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		return joinRoom(ctx, sqlgen.New(tx), room, agent)
	})
}
func joinRoom(ctx context.Context, q *sqlgen.Queries, room, agent string) error {
	if err := requireActor(ctx, q, false); err != nil {
		return err
	}
	if _, e := q.GetRoom(ctx, room); e != nil {
		return missing(e, "The room")
	}
	a, e := q.GetAgent(ctx, agent)
	if e != nil {
		return missing(e, "The agent")
	}
	if !a.Enabled {
		return problem("invalid_agent", "Enable the agent before joining a room.")
	}
	if a.OwnerUserID.Valid {
		if err := requireObjectOwner(ctx, q, a.OwnerUserID.String); err != nil {
			return err
		}
		if err := enabledOwner(ctx, q, a.OwnerUserID.String); err != nil {
			return err
		}
	}
	if e := q.JoinRoom(ctx, sqlgen.JoinRoomParams{RoomID: room, AgentID: agent, CreatedAt: now()}); e != nil {
		return e
	}
	return event(ctx, q, "membership.updated", room, agent)
}
func (s *Store) LeaveRoom(ctx context.Context, room, agent string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		member, err := q.GetAgent(ctx, agent)
		if err != nil {
			return missing(err, "The agent")
		}
		if err = requireObjectOwner(ctx, q, member.OwnerUserID.String); err != nil {
			return err
		}
		if _, e := q.GetRoom(ctx, room); e != nil {
			return missing(e, "The room")
		}
		if e := q.LeaveRoom(ctx, sqlgen.LeaveRoomParams{RoomID: room, AgentID: agent}); e != nil {
			return e
		}
		if e := q.ClearDefaultAgent(ctx, sqlgen.ClearDefaultAgentParams{ID: room, DefaultAgentID: nullableParent(agent)}); e != nil {
			return e
		}
		if err := revokeRuns(ctx, q, "The agent left this room.", func(run Run) bool { return run.RoomID == room && run.AgentID == agent }); err != nil {
			return err
		}
		return event(ctx, q, "membership.updated", room, agent)
	})
}
func (s *Store) ListWorkspaceEvents(ctx context.Context, after int64) ([]WorkspaceEvent, error) {
	rows, e := s.queries().ListWorkspaceEvents(ctx, after)
	out := make([]WorkspaceEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, WorkspaceEvent{ID: r.ID, Type: r.Type, RoomID: r.RoomID, EntityID: r.EntityID, CreatedAt: r.CreatedAt})
	}
	return out, e
}

// CreateRoomWithResource publishes the empty directory metadata and its room
// atomically. The caller owns removal of filesystem artifacts after failure.
func (s *Store) CreateRoomWithResource(ctx context.Context, room Room, resource Resource) (Room, error) {
	if resource.ID == "" {
		resource.ID = newID()
	}
	if len(resource.Environment) == 0 {
		resource.Environment = json.RawMessage(`{}`)
	}
	if !validText(resource.Name, 240, false) || !validText(resource.SourcePath, 4096, true) || !commitValid(resource.CurrentCommit) || !objectJSON(resource.Environment) {
		return Room{}, problem("invalid_resource", "The resource metadata is invalid.")
	}
	resource.CreatedAt = now()
	resource.UpdatedAt = resource.CreatedAt
	room.ResourceID = resource.ID
	if room.ID == "" {
		room.ID = newID()
	}
	room.CreatedAt = now()
	room.UpdatedAt = room.CreatedAt
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := createResource(ctx, q, resource); err != nil {
			return err
		}
		return insertRoom(ctx, q, &room)
	})
	return room, err
}
func insertRoom(ctx context.Context, q *sqlgen.Queries, r *Room) error {
	if err := validateRoom(ctx, q, r, true); err != nil {
		return err
	}
	if err := q.CreateRoom(ctx, sqlgen.CreateRoomParams{ID: r.ID, Name: r.Name, Description: r.Description, ResourceID: r.ResourceID, DefaultAgentID: nullableParent(r.DefaultAgentID), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}); err != nil {
		return err
	}
	if r.DefaultAgentID != "" {
		if err := joinRoom(ctx, q, r.ID, r.DefaultAgentID); err != nil {
			return err
		}
	}
	return event(ctx, q, "room.updated", r.ID, r.ID)
}

// SeedRoom creates all initial collaboration records in one transaction. Existing
// rooms and user choices are never reseeded on restart.
func (s *Store) SeedRoom(ctx context.Context, resource Resource) (out Room, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		rooms, e := q.ListRooms(ctx)
		if e != nil {
			return e
		}
		if len(rooms) > 0 {
			out = roomRow(rooms[0])
			return nil
		}
		if resource.ID == "" {
			resource.ID = newID()
		}
		if len(resource.Environment) == 0 {
			resource.Environment = json.RawMessage(`{}`)
		}
		if !validText(resource.Name, 240, false) || !commitValid(resource.CurrentCommit) || !objectJSON(resource.Environment) {
			return problem("invalid_resource", "The initial resource is invalid.")
		}
		resource.CreatedAt = now()
		resource.UpdatedAt = resource.CreatedAt
		if _, e := q.GetResource(ctx, resource.ID); errors.Is(e, sql.ErrNoRows) {
			if e = createResource(ctx, q, resource); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
		agent, e := q.GetAgentByHandle(ctx, "assistant")
		if errors.Is(e, sql.ErrNoRows) {
			agent = sqlgen.Agent{ID: newID(), Name: "Assistant", Handle: "assistant", Icon: "agent", Instructions: "Help the user with general questions, documents, and project work. Report results accurately and explain any limitations.", Access: "write", Enabled: true, Revision: 1, CreatedAt: now(), UpdatedAt: now()}
			if e = q.CreateAgent(ctx, sqlgen.CreateAgentParams{ID: agent.ID, Name: agent.Name, Handle: agent.Handle, Icon: agent.Icon, Instructions: agent.Instructions, Access: agent.Access, Enabled: agent.Enabled, Revision: agent.Revision, CreatedAt: agent.CreatedAt, UpdatedAt: agent.UpdatedAt}); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
		out = Room{ID: newID(), Name: "general", Description: "A place to work together.", ResourceID: resource.ID, DefaultAgentID: agent.ID, CreatedAt: now(), UpdatedAt: now()}
		return insertRoom(ctx, q, &out)
	})
	return
}
