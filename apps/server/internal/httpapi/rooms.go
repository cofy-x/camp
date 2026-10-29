package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// RoomService owns application commands that cross process and file boundaries.
// Read-only persistence endpoints remain directly backed by the store.
type RoomService interface {
	CreateRoom(context.Context, string, string) (storage.Room, error)
	ImportResource(context.Context, string, string, json.RawMessage) (storage.Resource, error)
	Send(context.Context, string, storage.SendMessageInput) (storage.SendResult, error)
	Cancel(context.Context, string) (storage.Run, error)
	Leave(context.Context, string, string) error
	SaveAgent(context.Context, storage.Agent) (storage.Agent, error)
	CreateRoomAgent(context.Context, string, storage.Agent) (storage.Agent, error)
	ResourceFiles(context.Context, string, string) ([]string, string, error)
	ResourceFile(context.Context, string, string, string) ([]byte, error)
	RunPatch(context.Context, string) ([]byte, error)
	Status() any
}
type runReader interface {
	Get(context.Context, string) (storage.Run, error)
}
type roomRunsReader interface {
	ListRoomRuns(context.Context, string) ([]storage.Run, error)
}
type resourceUpdater interface {
	UpdateResource(context.Context, storage.Resource) (storage.Resource, error)
}

func localRoomRequest(r *http.Request) bool {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil || !net.ParseIP(host).IsLoopback() {
		return false
	}
	host = r.Host
	if h, _, e := net.SplitHostPort(host); e == nil {
		host = h
	}
	if host != "localhost" && !net.ParseIP(strings.Trim(host, "[]")).IsLoopback() {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, e := url.Parse(origin)
		if e != nil || u.Host != r.Host || u.Scheme != "http" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return false
		}
	}
	return true
}
func localRoomHandler(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !localRoomRequest(r) {
			writeError(w, 403, "workspace_forbidden", "Workspace requires local, same-origin access.")
			return
		}
		h(w, r)
	}
}
func (a *API) runtime(w http.ResponseWriter) bool {
	if a.rooms == nil {
		writeError(w, 503, "execution_unavailable", "The Workspace runtime is not configured.")
		return false
	}
	return true
}
func queryCursor(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return 0, true
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n < 0 {
		writeError(w, 400, "invalid_cursor", "Use a nonnegative numeric cursor.")
		return 0, false
	}
	return n, true
}
func (a *API) roomRoutes(mux *http.ServeMux) {
	register := func(route string, h http.HandlerFunc) { mux.HandleFunc(route, a.authorizeRoute(route, h)) }
	register("GET /api/workspace/status", func(w http.ResponseWriter, r *http.Request) {
		if !a.runtime(w) {
			return
		}
		a.write(w, 200, a.rooms.Status())
	})
	register("GET /api/rooms", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.ListRooms(r.Context())
		a.respond(w, r, map[string]any{"rooms": v}, e)
	})
	register("POST /api/rooms", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !decode(w, r, &in, 8192) || !a.runtime(w) {
			return
		}
		v, e := a.rooms.CreateRoom(r.Context(), in.Name, in.Description)
		a.respond(w, r, map[string]any{"room": v}, e)
	})
	register("PATCH /api/rooms/{id}", a.updateRoom)
	register("POST /api/rooms/{id}/uploads", a.uploadRoomFile)
	register("GET /api/rooms/{id}/uploads", a.roomUploads)
	register("GET /api/rooms/{id}/uploads/{requestId}", a.roomUpload)
	register("GET /api/agents", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.ListAgents(r.Context())
		a.respond(w, r, map[string]any{"agents": v}, e)
	})
	register("POST /api/agents", a.createAgent)
	register("POST /api/rooms/{id}/agents", a.createRoomAgent)
	register("PATCH /api/agents/{id}", a.updateAgent)
	register("GET /api/rooms/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.ListMembers(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"members": v}, e)
	})
	register("PUT /api/rooms/{id}/members/{agentId}", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !decode(w, r, &in, 1024) {
			return
		}
		e := a.store.JoinRoom(r.Context(), r.PathValue("id"), r.PathValue("agentId"))
		a.respond(w, r, map[string]bool{"ok": true}, e)
	})
	register("DELETE /api/rooms/{id}/members/{agentId}", func(w http.ResponseWriter, r *http.Request) {
		if !a.runtime(w) {
			return
		}
		e := a.rooms.Leave(r.Context(), r.PathValue("id"), r.PathValue("agentId"))
		a.respond(w, r, map[string]bool{"ok": true}, e)
	})
	register("GET /api/rooms/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		after, ok := queryCursor(w, r, "after")
		if !ok {
			return
		}
		v, e := a.store.ListMessages(r.Context(), r.PathValue("id"), r.URL.Query().Get("threadRootId"), after)
		a.respond(w, r, map[string]any{"messages": v}, e)
	})
	register("POST /api/rooms/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var in storage.SendMessageInput
		if !decode(w, r, &in, 65536) || !a.runtime(w) {
			return
		}
		v, e := a.rooms.Send(r.Context(), r.PathValue("id"), in)
		a.respond(w, r, v, e)
	})
	register("GET /api/rooms/{id}/messages/{messageId}", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.GetRoomMessage(r.Context(), r.PathValue("id"), r.PathValue("messageId"))
		a.respond(w, r, map[string]any{"message": v}, e)
	})
	register("GET /api/rooms/{id}/runs", func(w http.ResponseWriter, r *http.Request) {
		var v []storage.Run
		var e error
		if reader, ok := a.rooms.(roomRunsReader); ok {
			v, e = reader.ListRoomRuns(r.Context(), r.PathValue("id"))
		} else {
			v, e = a.store.ListRoomRuns(r.Context(), r.PathValue("id"))
			if e == nil {
				if reader, ok := a.rooms.(runReader); ok {
					for i := range v {
						v[i], e = reader.Get(r.Context(), v[i].ID)
						if e != nil {
							break
						}
					}
				}
			}
		}
		a.respond(w, r, map[string]any{"runs": v}, e)
	})
	register("GET /api/resources", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.ListResources(r.Context())
		a.respond(w, r, map[string]any{"resources": v}, e)
	})
	register("POST /api/resources", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name        string          `json:"name"`
			Path        string          `json:"path"`
			Environment json.RawMessage `json:"environment"`
		}
		if !decode(w, r, &in, 65536) || !a.runtime(w) {
			return
		}
		resolved, err := a.importPolicy.Resolve(in.Path)
		if err != nil {
			a.settingsRespond(w, r, nil, err)
			return
		}
		v, e := a.rooms.ImportResource(r.Context(), in.Name, resolved, in.Environment)
		a.respond(w, r, map[string]any{"resource": v}, e)
	})
	register("PATCH /api/resources/{id}", a.updateResource)
	register("POST /api/resources/{id}/document", a.prepareDocument)
	register("GET /api/resources/{id}/document", a.latestDocument)
	register("GET /api/documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, err := a.store.GetDocument(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"document": d}, err)
	})
	register("GET /api/resources/{id}/files", func(w http.ResponseWriter, r *http.Request) {
		if !a.runtime(w) {
			return
		}
		if len(r.URL.Query()["commit"]) > 1 {
			writeError(w, 400, "invalid_reference", "Provide one resource revision.")
			return
		}
		v, commit, e := a.rooms.ResourceFiles(r.Context(), r.PathValue("id"), r.URL.Query().Get("commit"))
		a.respond(w, r, map[string]any{"files": v, "commit": commit}, e)
	})
	register("GET /api/resources/{id}/file", func(w http.ResponseWriter, r *http.Request) {
		if !a.runtime(w) {
			return
		}
		if len(r.URL.Query()["commit"]) > 1 || len(r.URL.Query()["path"]) > 1 {
			writeError(w, 400, "invalid_reference", "Provide one file path and resource revision.")
			return
		}
		v, e := a.rooms.ResourceFile(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"), r.URL.Query().Get("commit"))
		if e != nil {
			a.fail(w, r, e)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment")
		_, _ = w.Write(v)
	})
	register("GET /api/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		var v storage.Run
		var e error
		if reader, ok := a.rooms.(runReader); ok {
			v, e = reader.Get(r.Context(), r.PathValue("id"))
		} else {
			v, e = a.store.GetRun(r.Context(), r.PathValue("id"))
		}
		a.respond(w, r, map[string]any{"run": v}, e)
	})
	register("POST /api/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !decode(w, r, &in, 1024) || !a.runtime(w) {
			return
		}
		v, e := a.rooms.Cancel(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"run": v}, e)
	})
	register("GET /api/runs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		after, ok := queryCursor(w, r, "after")
		if !ok {
			return
		}
		v, e := a.store.ListRunEvents(r.Context(), r.PathValue("id"), after)
		a.respond(w, r, map[string]any{"events": v}, e)
	})
	register("GET /api/runs/{id}/patch", func(w http.ResponseWriter, r *http.Request) {
		if !a.runtime(w) {
			return
		}
		v, e := a.rooms.RunPatch(r.Context(), r.PathValue("id"))
		if e != nil {
			a.fail(w, r, e)
			return
		}
		w.Header().Set("Content-Type", "text/x-patch; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="result.patch"`)
		_, _ = w.Write(v)
	})
	register("GET /api/workspace/events", a.workspaceEvents)
}
func (a *API) updateRoom(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name           *string `json:"name"`
		Description    *string `json:"description"`
		ResourceID     *string `json:"resourceId"`
		DefaultAgentID *string `json:"defaultAgentId"`
	}
	if !decode(w, r, &in, 8192) {
		return
	}
	v, e := a.store.GetRoom(r.Context(), r.PathValue("id"))
	if e != nil {
		a.fail(w, r, e)
		return
	}
	if in.Name != nil {
		v.Name = *in.Name
	}
	if in.Description != nil {
		v.Description = *in.Description
	}
	if in.ResourceID != nil {
		v.ResourceID = *in.ResourceID
	}
	if in.DefaultAgentID != nil {
		v.DefaultAgentID = *in.DefaultAgentID
	}
	v, e = a.store.UpdateRoom(r.Context(), v)
	a.respond(w, r, map[string]any{"room": v}, e)
}

type agentInput struct {
	PreferredRunnerID *string `json:"preferredRunnerId"`
	Name              *string `json:"name"`
	Handle            *string `json:"handle"`
	Icon              *string `json:"icon"`
	Instructions      *string `json:"instructions"`
	Access            *string `json:"access"`
	Enabled           *bool   `json:"enabled"`
	Revision          *int64  `json:"revision"`
}

func applyAgent(a storage.Agent, in agentInput) storage.Agent {
	if in.PreferredRunnerID != nil {
		a.PreferredRunnerID = *in.PreferredRunnerID
	}
	if in.Name != nil {
		a.Name = *in.Name
	}
	if in.Handle != nil {
		a.Handle = *in.Handle
	}
	if in.Icon != nil {
		a.Icon = *in.Icon
	}
	if in.Instructions != nil {
		a.Instructions = *in.Instructions
	}
	if in.Access != nil {
		a.Access = *in.Access
	}
	if in.Enabled != nil {
		a.Enabled = *in.Enabled
	}
	if in.Revision != nil {
		a.Revision = *in.Revision
	}
	return a
}
func (a *API) createAgent(w http.ResponseWriter, r *http.Request) {
	var in agentInput
	if !decode(w, r, &in, 16384) || !a.runtime(w) {
		return
	}
	v, e := a.rooms.SaveAgent(r.Context(), applyAgent(storage.Agent{Access: "write", Enabled: true}, in))
	a.respond(w, r, map[string]any{"agent": v}, e)
}
func (a *API) createRoomAgent(w http.ResponseWriter, r *http.Request) {
	var in agentInput
	if !decode(w, r, &in, 16384) || !a.runtime(w) {
		return
	}
	v, e := a.rooms.CreateRoomAgent(r.Context(), r.PathValue("id"), applyAgent(storage.Agent{Access: "write", Enabled: true}, in))
	a.respond(w, r, map[string]any{"agent": v}, e)
}
func (a *API) updateAgent(w http.ResponseWriter, r *http.Request) {
	var in agentInput
	if !decode(w, r, &in, 16384) || !a.runtime(w) {
		return
	}
	if in.Revision == nil {
		writeError(w, 400, "invalid_agent", "Include the agent revision when saving changes.")
		return
	}
	v, e := a.store.GetAgent(r.Context(), r.PathValue("id"))
	if e != nil {
		a.fail(w, r, e)
		return
	}
	v, e = a.rooms.SaveAgent(r.Context(), applyAgent(v, in))
	a.respond(w, r, map[string]any{"agent": v}, e)
}
func (a *API) updateResource(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        *string         `json:"name"`
		Environment json.RawMessage `json:"environment"`
	}
	if !decode(w, r, &in, 65536) {
		return
	}
	v, e := a.store.GetResource(r.Context(), r.PathValue("id"))
	if e != nil {
		a.fail(w, r, e)
		return
	}
	if in.Name != nil {
		v.Name = *in.Name
	}
	if in.Environment != nil {
		v.Environment = in.Environment
	}
	if updater, ok := a.rooms.(resourceUpdater); ok {
		v, e = updater.UpdateResource(r.Context(), v)
	} else {
		v, e = a.store.UpdateResource(r.Context(), v)
	}
	a.respond(w, r, map[string]any{"resource": v}, e)
}

// The event log is the source of truth; polling it only wakes this connection.
// Reconnection resumes after the last committed event without process-local state.
func (a *API) workspaceEvents(w http.ResponseWriter, r *http.Request) {
	if a.streams.Err() != nil {
		writeError(w, 503, "service_stopping", "Camp is restarting. Reconnect shortly.")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(a.streams, cancel)
	defer stop()
	after, ok := queryCursor(w, r, "after")
	if !ok {
		return
	}
	if h := r.Header.Get("Last-Event-ID"); h != "" {
		n, e := strconv.ParseInt(h, 10, 64)
		if e != nil || n < 0 {
			writeError(w, 400, "invalid_cursor", "Last-Event-ID must be a nonnegative number.")
			return
		}
		after = n
	}
	events, e := a.store.ListWorkspaceEvents(ctx, after)
	if e != nil {
		a.fail(w, r, e)
		return
	}
	control := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	if e := control.SetWriteDeadline(time.Now().Add(30 * time.Second)); e != nil && e != http.ErrNotSupported {
		return
	}
	if _, e := fmt.Fprint(w, ": connected\n\n"); e != nil {
		return
	}
	if e := control.Flush(); e != nil {
		return
	}
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		for _, event := range events {
			if ctx.Err() != nil {
				return
			}
			b, e := json.Marshal(event)
			if e != nil {
				return
			}
			_ = control.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, e = fmt.Fprintf(w, "id: %d\nevent: workspace\ndata: %s\n\n", event.ID, b); e != nil {
				return
			}
			after = event.ID
		}
		if len(events) > 0 {
			if e := control.Flush(); e != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			_ = control.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, e := fmt.Fprint(w, ": heartbeat\n\n"); e != nil {
				return
			}
			if e := control.Flush(); e != nil {
				return
			}
		case <-ticker.C:
		}
		events, e = a.store.ListWorkspaceEvents(ctx, after)
		if e != nil {
			return
		}
	}
}
