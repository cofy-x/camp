// Package remoteruns owns server-side admission and publication for remote executors.
// No native runtime or host command is started by this package.
package remoteruns

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type Config struct {
	DataDir       string
	Models        runs.Config
	AllowFixture  bool
	Concurrency   int
	LeaseDuration time.Duration
}

type Manager struct {
	store       *storage.Store
	files       *resources.Manager
	models      *configuration.Models
	diagnostics *configuration.Diagnostics
	config      Config
	logger      *slog.Logger
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	wake        chan struct{}
	mu          sync.Mutex
	peers       map[string]*peer
	proxyCalls  map[string]map[string]context.CancelFunc
	closed      bool
	lastGC      time.Time // scheduler-owned
}

func New(ctx context.Context, store *storage.Store, config Config, logger *slog.Logger) (*Manager, error) {
	directory, err := filepath.Abs(config.DataDir)
	if err != nil {
		return nil, err
	}
	config.DataDir = directory
	if logger == nil {
		logger = slog.Default()
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 2
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = time.Minute
	}
	if err := os.MkdirAll(filepath.Join(config.DataDir, "remote"), 0700); err != nil {
		return nil, err
	}
	overrides := configuration.Overrides{}
	if config.Models.ModelOverrides != nil {
		overrides = *config.Models.ModelOverrides
	}
	models, err := configuration.Open(filepath.Join(config.DataDir, "settings"), config.Models.ModelDefaults(), overrides)
	if err != nil {
		return nil, err
	}
	diagnostics, err := configuration.OpenDiagnostics(filepath.Join(config.DataDir, "settings"))
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m := &Manager{store: store, files: resources.New(filepath.Join(config.DataDir, "resources")), models: models, diagnostics: diagnostics, config: config, logger: logger, ctx: life, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1), peers: map[string]*peer{}, proxyCalls: map[string]map[string]context.CancelFunc{}}
	if err = m.recover(ctx); err != nil {
		cancel()
		return nil, err
	}
	go m.schedule()
	return m, nil
}

func newID() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	return hex.EncodeToString(bytes[:])
}
func secret() string {
	var bytes [32]byte
	_, _ = rand.Read(bytes[:])
	return hex.EncodeToString(bytes[:])
}
func digest(b []byte) string             { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func problem(code, message string) error { return &storage.Error{Code: code, Message: message} }
func (m *Manager) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) Availability() (bool, string, runs.Budget) {
	_, model := m.models.Snapshot()
	budget := runs.Budget{MaxTokens: model.MaxTokens, MaxSteps: model.MaxSteps, TimeoutSeconds: model.TimeoutSeconds}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return false, "The server is stopping.", budget
	}
	if model.APIKey == "" && !m.config.AllowFixture {
		return false, "Configure a model connection in Settings.", budget
	}
	return true, "", budget
}

func (m *Manager) Send(ctx context.Context, room string, input storage.SendMessageInput) (storage.SendResult, error) {
	if result, found, err := m.store.FindSendMessage(ctx, room, input); err != nil || found {
		return result, err
	}
	ids, err := m.store.MessageRecipientIDs(ctx, room, input)
	if err != nil {
		return storage.SendResult{}, err
	}
	if len(ids) > 0 {
		if available, reason, _ := m.Availability(); !available {
			return storage.SendResult{}, problem("execution_unavailable", reason)
		}
		for _, agentID := range ids {
			runner, err := m.store.ResolveAgentRunner(ctx, room, agentID)
			if err != nil {
				return storage.SendResult{}, err
			}
			var capabilities runnerprotocol.Capabilities
			_ = json.Unmarshal(runner.Capabilities, &capabilities)
			if runner.Runtime == "fixture" && !m.config.AllowFixture {
				return storage.SendResult{}, problem("runner_unavailable", "Test executors are disabled on this server.")
			}
			if capabilities.Executor != "" && !capabilities.Ready {
				return storage.SendResult{}, problem("runner_unavailable", capabilities.Reason)
			}
		}
	}
	for _, ref := range input.References {
		if ref.Kind != "file" {
			continue
		}
		if _, err := m.store.ValidateFileReference(ctx, room, ref); err != nil {
			return storage.SendResult{}, err
		}
		if _, err := m.files.ReadFile(ctx, ref.ResourceID, ref.Commit, ref.Path); err != nil {
			return storage.SendResult{}, problem("invalid_reference", "The referenced immutable file is unavailable.")
		}
	}
	revision, model := m.models.Snapshot()
	result, err := m.store.SendMessage(ctx, room, input, storage.Budget{Model: model.Model, RuntimeKey: revision, MaxTokens: model.MaxTokens, MaxSteps: model.MaxSteps, TimeoutSeconds: model.TimeoutSeconds, RequireRunner: true})
	if err == nil {
		m.Wake()
	}
	return result, err
}

func (m *Manager) Get(ctx context.Context, id string) (storage.Run, error) {
	run, err := m.store.GetRun(ctx, id)
	if err == nil && run.Status == "queued" {
		switch {
		case run.RunnerID == "":
			run.WaitingReason = "Choose a runner and submit a new request."
		case !m.IsOnline(run.RunnerID):
			run.WaitingReason = "Waiting for the assigned runner to connect."
		default:
			run.WaitingReason = "Waiting for an available execution slot or file writer."
		}
	}
	return run, err
}
func (m *Manager) AgentStates(ctx context.Context) (map[string]string, error) {
	queued, err := m.store.ListQueuedRuns(ctx)
	if err != nil {
		return nil, err
	}
	active, err := m.store.ListUnfinishedRuns(ctx)
	if err != nil {
		return nil, err
	}
	states := map[string]string{}
	for _, r := range queued {
		states[r.AgentID] = "queued"
	}
	for _, r := range active {
		states[r.AgentID] = r.Status
	}
	return states, nil
}
func (m *Manager) CancelMatching(ctx context.Context, room, agent string) error {
	queued, err := m.store.ListQueuedRuns(ctx)
	if err != nil {
		return err
	}
	active, err := m.store.ListUnfinishedRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range append(queued, active...) {
		if run.AgentID == agent && (room == "" || run.RoomID == room) {
			if _, err := m.Cancel(ctx, run.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) WithResourceWriter(ctx context.Context, id string, apply func() error) error {
	return m.WithResourceLeaseWriter(ctx, id, func(context.Context) error { return apply() })
}
func (m *Manager) WithResourceLeaseWriter(ctx context.Context, id string, apply func(context.Context) error) error {
	lease, err := m.store.AcquireResourceLease(ctx, id, "human:"+newID(), time.Now().Add(time.Minute))
	if err != nil {
		return err
	}
	defer m.store.ReleaseResourceLease(context.WithoutCancel(ctx), lease)
	return apply(storage.WithResourceLease(ctx, lease))
}

func (m *Manager) ModelSettings() configuration.View { return m.models.View() }
func (m *Manager) UpdateModelSettings(ctx context.Context, input configuration.Update) (configuration.View, error) {
	value, err := m.models.Update(input)
	if err == nil {
		m.Wake()
		err = m.store.RecordManagementEvent(ctx, "model.updated", value.Revision, "Private model configuration version saved.")
	}
	return value, err
}
func (m *Manager) EnvironmentSettings() runs.EnvironmentSettings {
	return runs.EnvironmentSettings{Platform: "Remote Linux runners", NodeVersion: "Runner capability", PackageManager: "Runner capability", CommandNetwork: false, Concurrency: m.config.Concurrency, Checks: "Resource-specific checks on the assigned runner.", Limits: map[string]int{"maxFiles": 10000, "maxFileBytes": 8 << 20, "maxProjectBytes": 128 << 20}}
}
func (m *Manager) MaxConcurrent() int { return m.config.Concurrency }
func (m *Manager) StorageSettings() runs.StorageSettings {
	return runs.StorageSettings{DataDirectory: m.config.DataDir, Files: "Immutable Git resource versions", Versions: "SQLite publication and review records", Backups: "Stop the server and back up its complete data directory."}
}
func (m *Manager) CheckReadiness(ctx context.Context, recheck bool) (runs.Readiness, error) {
	revision, model := m.models.Snapshot()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	modelState, modelDetail := "missing", "Configure a model connection."
	if model.APIKey != "" {
		modelState, modelDetail = "ready", "Model configuration is present; connectivity is reported separately."
	}
	runners, err := m.ListRunners(ctx)
	if err != nil {
		return runs.Readiness{}, err
	}
	ready := 0
	for _, r := range runners {
		var c runnerprotocol.Capabilities
		_ = json.Unmarshal(r.Capabilities, &c)
		if r.Online && r.Enabled && !r.Revoked && c.Ready {
			ready++
		}
	}
	runnerState, runnerDetail := "missing", "Connect an execution machine in Runners."
	if ready > 0 {
		runnerState, runnerDetail = "ready", "An execution runner is connected."
	}
	connection := m.diagnostics.Get("connection:" + revision)
	if connection.Status == "" {
		connection = configuration.Diagnostic{Status: "unknown", Detail: "No model connection has been observed."}
	}
	return runs.Readiness{Ready: ready > 0 && (model.APIKey != "" || m.config.AllowFixture), CheckedAt: stamp, Components: []runs.ReadinessComponent{{ID: "model", Label: "Model", Diagnostic: configuration.Diagnostic{Status: modelState, Detail: modelDetail, CheckedAt: stamp}}, {ID: "connection", Label: "Model connection", Diagnostic: connection}, {ID: "execution", Label: "Runners", Diagnostic: configuration.Diagnostic{Status: runnerState, Detail: runnerDetail, CheckedAt: stamp}}}}, nil
}

type RunnerView struct {
	EarlierTaskRunID       string `json:"earlierTaskRunId,omitempty"`
	EarlierTaskSucceededAt string `json:"earlierTaskSucceededAt,omitempty"`
	FirstTaskRunID         string `json:"firstTaskRunId"`
	FirstTaskSucceededAt   string `json:"firstTaskSucceededAt"`
	ModelConfigured        bool   `json:"modelConfigured"`
	ModelCheckedAt         string `json:"modelCheckedAt"`
	storage.Runner
	Online bool `json:"online"`
}

func (m *Manager) ListRunners(ctx context.Context) ([]RunnerView, error) {
	rows, err := m.store.ListRunners(ctx)
	out := make([]RunnerView, 0, len(rows))
	for _, r := range rows {
		view := RunnerView{Runner: r, Online: m.IsOnline(r.ID)}
		_, model := m.models.Snapshot()
		view.ModelConfigured = model.APIKey != ""
		view.ModelCheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if r.Runtime == "native" {
			var e error
			view.FirstTaskRunID, view.FirstTaskSucceededAt, e = m.store.FirstRunnerSuccess(ctx, r.ID)
			if e != nil {
				return nil, e
			}
			if view.FirstTaskRunID == "" {
				view.EarlierTaskRunID, view.EarlierTaskSucceededAt, e = m.store.FirstRunnerUnrecordedSuccess(ctx, r.ID)
				if e != nil {
					return nil, e
				}
			}
		}
		out = append(out, view)
	}
	return out, err
}
func (m *Manager) IsOnline(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.peers[id]
	return p != nil && time.Since(p.lastSeen) < 30*time.Second
}
func (m *Manager) StopRunner(id string) {
	m.mu.Lock()
	p := m.peers[id]
	m.mu.Unlock()
	if p != nil {
		p.close()
	}
	m.Wake()
}
func (m *Manager) stopProxy(id string) {
	m.mu.Lock()
	for _, cancel := range m.proxyCalls[id] {
		cancel()
	}
	m.mu.Unlock()
}
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	for _, p := range m.peers {
		p.close()
	}
	for _, calls := range m.proxyCalls {
		for _, cancel := range calls {
			cancel()
		}
	}
	m.mu.Unlock()
	m.cancel()
	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) redact(value string) string {
	return m.models.Redact(value)
}
func terminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled" || status == "interrupted"
}
func expired(value string) bool {
	t, err := time.Parse(time.RFC3339Nano, value)
	return err != nil || !time.Now().Before(t)
}
func errorCode(err error) string {
	var p *storage.Error
	if errors.As(err, &p) {
		return p.Code
	}
	return "internal_error"
}
