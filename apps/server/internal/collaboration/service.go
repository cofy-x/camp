// Package collaboration connects room membership and messages to managed files
// and execution. Storage owns validation and transactions; runs owns processes.
package collaboration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/documents"
	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type Service struct {
	documents *documents.Service
	store     *storage.Store
	runs      RunManager
	files     *resources.Manager
	mu        sync.Mutex
}

type RunManager interface {
	Availability() (bool, string, runs.Budget)
	AgentStates(context.Context) (map[string]string, error)
	Send(context.Context, string, storage.SendMessageInput) (storage.SendResult, error)
	Get(context.Context, string) (storage.Run, error)
	Cancel(context.Context, string) (storage.Run, error)
	CancelMatching(context.Context, string, string) error
}

func New(ctx context.Context, store *storage.Store, manager RunManager, dataDir string, preparers ...*documents.Service) (*Service, error) {
	s := &Service{store: store, runs: manager, files: resources.New(filepath.Join(dataDir, "resources"))}
	if len(preparers) > 0 {
		s.documents = preparers[0]
	}
	rooms, err := store.ListRooms(ctx)
	if err != nil {
		return nil, err
	}
	if len(rooms) == 0 {
		id := newID()
		created, err := s.files.Create(ctx, id)
		if err != nil {
			return nil, err
		}
		_, err = store.SeedRoom(ctx, storage.Resource{ID: id, Name: "General files", CurrentCommit: created.Commit})
		if err != nil {
			_ = s.files.DiscardImported(id)
			return nil, err
		}
	}
	return s, nil
}

func newID() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	return hex.EncodeToString(bytes[:])
}

func (s *Service) Status() any {
	available, reason, budget := s.runs.Availability()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	states, err := s.runs.AgentStates(ctx)
	if err != nil {
		available = false
		reason = "Agent activity could not be loaded. Check server storage and try again."
	}
	return struct {
		Available     bool              `json:"available"`
		Reason        string            `json:"reason"`
		Budget        runs.Budget       `json:"budget"`
		MaxConcurrent int               `json:"maxConcurrent"`
		AgentStates   map[string]string `json:"agentStates"`
	}{available, reason, budget, s.maxConcurrent(), states}
}
func (s *Service) maxConcurrent() int {
	if manager, ok := s.runs.(interface{ MaxConcurrent() int }); ok {
		return manager.MaxConcurrent()
	}
	return 2
}

func (s *Service) CreateRoom(ctx context.Context, name, description string) (storage.Room, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := newID()
	created, err := s.files.Create(ctx, id)
	if err != nil {
		return storage.Room{}, err
	}
	resource := storage.Resource{ID: id, Name: name + " files", CurrentCommit: created.Commit}
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		_ = s.files.DiscardImported(id)
		return storage.Room{}, err
	}
	room := storage.Room{Name: name, Description: description, ResourceID: resource.ID}
	for _, agent := range agents {
		if agent.Handle == "assistant" && agent.Enabled && agent.OwnerUserID == "" {
			room.DefaultAgentID = agent.ID
			break
		}
	}
	saved, err := s.store.CreateRoomWithResource(ctx, room, resource)
	if err != nil {
		_ = s.files.DiscardImported(id)
	}
	return saved, err
}

func (s *Service) ImportResource(ctx context.Context, name, path string, environment json.RawMessage) (storage.Resource, error) {
	environment, profile, err := normalizedEnvironment(environment)
	if err != nil {
		return storage.Resource{}, err
	}
	id := newID()
	result, err := s.files.ImportWithPolicy(ctx, id, path, projectenv.SnapshotPolicy(profile))
	if err != nil {
		return storage.Resource{}, &storage.Error{Code: "invalid_resource", Message: err.Error()}
	}
	resource, err := s.store.CreateResource(ctx, storage.Resource{ID: id, Name: name, SourcePath: result.SourcePath, CurrentCommit: result.Commit, Environment: environment})
	if err != nil {
		_ = s.files.DiscardImported(id)
	}
	return resource, err
}

func (s *Service) UpdateResource(ctx context.Context, resource storage.Resource) (storage.Resource, error) {
	encoded, _, err := normalizedEnvironment(resource.Environment)
	if err != nil {
		return storage.Resource{}, err
	}
	resource.Environment = encoded
	return s.store.UpdateResource(ctx, resource)
}

func (s *Service) Send(ctx context.Context, room string, input storage.SendMessageInput) (storage.SendResult, error) {
	if _, found, err := s.store.FindSendMessage(ctx, room, input); err != nil {
		return storage.SendResult{}, err
	} else if found {
		return s.runs.Send(ctx, room, input)
	}
	if s.documents != nil {
		for _, ref := range input.References {
			if ref.Kind != "file" || ref.DocumentID != "" || storage.DocumentFormat(ref.Path) == "" {
				continue
			}
			if _, err := s.store.ValidateFileReference(ctx, room, ref); err != nil {
				return storage.SendResult{}, err
			}
			if _, err := s.documents.Prepare(ctx, ref.ResourceID, ref.Commit, ref.Path, false); err != nil {
				return storage.SendResult{}, err
			}
		}
	}
	return s.runs.Send(ctx, room, input)
}
func (s *Service) Get(ctx context.Context, id string) (storage.Run, error) {
	return s.runs.Get(ctx, id)
}
func (s *Service) ListRoomRuns(ctx context.Context, room string) ([]storage.Run, error) {
	items, err := s.store.ListRoomRuns(ctx, room)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i], err = s.runs.Get(ctx, items[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}
func (s *Service) Cancel(ctx context.Context, id string) (storage.Run, error) {
	return s.runs.Cancel(ctx, id)
}

func (s *Service) Leave(ctx context.Context, room, agent string) error {
	if err := s.store.LeaveRoom(ctx, room, agent); err != nil {
		return err
	}
	return s.runs.CancelMatching(ctx, room, agent)
}

func (s *Service) SaveAgent(ctx context.Context, agent storage.Agent) (storage.Agent, error) {
	saved, err := s.store.SaveAgent(ctx, agent)
	if err == nil && !saved.Enabled {
		err = s.runs.CancelMatching(ctx, "", saved.ID)
	}
	return saved, err
}

func (s *Service) CreateRoomAgent(ctx context.Context, room string, agent storage.Agent) (storage.Agent, error) {
	return s.store.CreateRoomAgent(ctx, room, agent)
}

func (s *Service) ResourceFiles(ctx context.Context, id, commit string) ([]string, string, error) {
	resource, err := s.store.ValidateResourceRevision(ctx, id, commit)
	if err != nil {
		return nil, "", err
	}
	if commit == "" {
		commit = resource.CurrentCommit
	}
	files, err := s.files.ListFiles(ctx, id, commit)
	return files, commit, err
}
func (s *Service) ResourceFile(ctx context.Context, id, path, commit string) ([]byte, error) {
	resource, err := s.store.ValidateResourceRevision(ctx, id, commit)
	if err != nil {
		return nil, err
	}
	if commit == "" {
		commit = resource.CurrentCommit
	}
	data, err := s.files.ReadFile(ctx, id, commit, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, &storage.Error{Code: "not_found", Message: "The resource file does not exist."}
	}
	if err != nil {
		return nil, &storage.Error{Code: "invalid_resource", Message: err.Error()}
	}
	return data, nil
}
func (s *Service) RunPatch(ctx context.Context, id string) ([]byte, error) {
	run, err := s.runs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if run.ResultCommit == "" {
		return nil, &storage.Error{Code: "invalid_run", Message: "This run has no captured file changes."}
	}
	return s.files.ExportPatch(ctx, run.ResourceID, run.BaselineCommit, run.ResultCommit)
}
func normalizedEnvironment(encoded json.RawMessage) (json.RawMessage, projectenv.Profile, error) {
	var fields map[string]json.RawMessage
	if len(encoded) > 0 {
		if err := json.Unmarshal(encoded, &fields); err != nil {
			return nil, projectenv.Profile{}, &storage.Error{Code: "invalid_resource", Message: "The environment configuration must be an object."}
		}
	}
	if len(fields) == 0 {
		return json.RawMessage(`{}`), projectenv.Profile{}, nil
	}
	var profile projectenv.Profile
	if err := json.Unmarshal(encoded, &profile); err != nil {
		return nil, profile, &storage.Error{Code: "invalid_resource", Message: "The environment configuration is invalid."}
	}
	if err := projectenv.Validate(profile); err != nil {
		return nil, profile, &storage.Error{Code: "invalid_resource", Message: err.Error()}
	}
	encoded, err := json.Marshal(profile)
	return encoded, profile, err
}
