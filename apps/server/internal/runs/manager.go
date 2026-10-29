package runs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/layerfs"
	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

const concurrencyLimit = 2

type Manager struct {
	store         *storage.Store
	resources     *resources.Manager
	config        Config
	dir           string
	lock          *os.File
	logger        *slog.Logger
	unavailable   string
	mu            sync.Mutex
	checkMu       sync.Mutex
	layerMu       sync.Mutex
	layerProjects map[string]*preparedUsage
	layerClock    uint64
	rechecking    bool
	active        map[string]*execution
	unsaved       map[string]storage.Run
	manualWriters map[string]bool
	bridge        *layerfs.Bridge
	docker        *layerfs.Docker
	helper        []byte
	models        *configuration.Models
	diagnostics   *configuration.Diagnostics
	closed        bool
	wake          chan struct{}
	stop          chan struct{}
	stopped       chan struct{}
	closeOnce     sync.Once
	closeErr      error
	executeRun    func(context.Context, *execution)
}

type execution struct {
	run                            storage.Run
	conversation                   storage.Conversation
	cancel                         context.CancelFunc
	done                           chan struct{}
	cancelled, interrupted         bool
	writeErr                       error
	assistant                      string
	environment                    *projectenv.Environment
	workspace                      layerfs.Workspace
	sandbox                        *layerfs.Sandbox
	tools                          *layerfs.Tools
	cleanupErr                     error
	layerProject                   string
	finishing                      bool
	model                          configuration.Model
	modelRequested, modelResponded int
}

func New(ctx context.Context, store *storage.Store, dataDir string, config Config, logger *slog.Logger) (*Manager, error) {
	ownership, err := AcquireOwnership(dataDir)
	if err != nil {
		return nil, err
	}
	defer ownership.Close()
	return NewWithOwnership(ctx, store, ownership, config, logger)
}

func NewWithOwnership(ctx context.Context, store *storage.Store, ownership *Ownership, config Config, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}
	dir, lock, err := ownership.take()
	if err != nil {
		return nil, err
	}
	m := &Manager{store: store, config: config, dir: dir, lock: lock, logger: logger, resources: resources.New(filepath.Join(filepath.Dir(dir), "resources")), active: map[string]*execution{}, unsaved: map[string]storage.Run{}, manualWriters: map[string]bool{}, wake: make(chan struct{}, 1), stop: make(chan struct{}), stopped: make(chan struct{})}
	overrides := configuration.Overrides{}
	if config.ModelOverrides != nil {
		overrides = *config.ModelOverrides
	}
	m.models, err = configuration.Open(filepath.Join(filepath.Dir(dir), "settings"), config.modelDefaults(), overrides)
	if err == nil {
		m.diagnostics, err = configuration.OpenDiagnostics(filepath.Join(filepath.Dir(dir), "settings"))
	}
	if err != nil {
		if lock != nil {
			_ = lock.Close()
		}
		return nil, err
	}

	m.executeRun = m.execute
	// Reset owned disposable stores even when model credentials were removed.
	// Recovery only reads confirmed host exports, never a crashed Store.
	layerFSProblem := m.openLayerFS(ctx)
	if err = m.cleanupScratch(); err == nil {
		err = m.recover(ctx)
	}
	if err == nil {
		err = m.cleanupTerminalScratch(ctx)
	}
	if err != nil {
		if m.bridge != nil {
			_ = m.bridge.Close()
		}
		if m.docker != nil {
			m.docker.Close()
		}
		if lock != nil {
			_ = lock.Close()
		}
		return nil, err
	}
	m.unavailable = m.checkAvailability(ctx)
	if m.unavailable == "" {
		m.unavailable = layerFSProblem
	}
	go m.schedule()
	return m, nil
}

func (m *Manager) recover(ctx context.Context) error {
	unfinished, err := m.store.ListUnfinishedRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range unfinished {
		captureCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		files, commit, captureErr := m.capture(captureCtx, run)
		cancel()
		run.Files, run.ResultCommit = files, commit
		m.redactFiles(run.Files)
		if captureErr != nil {
			run.Output = boundedOutput(run.Output, "[Recovery] "+m.redact(captureErr.Error())+"\n")
		}
		run.Output = m.redact(run.Output)
		run.Status = "interrupted"
		run.Error = "The service stopped before execution finished. Saved output and confirmed file exports were retained; unconfirmed sandbox changes were discarded. Send another message to continue."
		if _, err = m.store.FinishRun(ctx, run, "", false); err != nil {
			return err
		}
	}
	// A crash can occur between removing membership and cancelling its queue.
	// Reconcile authorization before checking whether the model is available.
	queued, err := m.store.ListQueuedRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range queued {
		agent, err := m.store.GetAgent(ctx, run.AgentID)
		if err != nil {
			return err
		}
		eligible := agent.Enabled && run.CancellationReason == ""
		if eligible && run.RequestedByUserID != "" {
			user, userErr := m.store.GetUser(ctx, run.RequestedByUserID)
			if userErr != nil {
				return userErr
			}
			eligible = user.Enabled
		}
		if eligible {
			members, err := m.store.ListMembers(ctx, run.RoomID)
			if err != nil {
				return err
			}
			eligible = false
			for _, member := range members {
				if member.ID == run.AgentID {
					eligible = true
					break
				}
			}
		}
		if !eligible {
			run.Status = "cancelled"
			run.Error = "The requester or agent was disabled, or the agent left this room before execution started."
			if run.CancellationReason != "" {
				run.Error = run.CancellationReason
			}
			if _, err := m.store.FinishRun(ctx, run, "", false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) checkAvailability(ctx context.Context) string {
	if _, err := exec.LookPath("git"); err != nil {
		return "Git could not be found. Install Git to enable isolated file work; the desktop and room discussions remain available."
	}
	var err error
	nodePath := m.config.NodePath
	if nodePath == "" {
		nodePath = "node"
	}
	if !filepath.IsAbs(nodePath) {
		nodePath, err = exec.LookPath(nodePath)
		if err != nil {
			return "The configured Node executable could not be found."
		}
	}
	nodePath, err = filepath.EvalSymlinks(nodePath)
	if err != nil {
		return "The configured Node executable could not be found."
	}
	m.config.NodePath = nodePath
	m.config.RuntimeDir, err = filepath.Abs(m.config.RuntimeDir)
	if err != nil {
		return "The DSH runtime directory is invalid."
	}
	manifest, err := os.ReadFile(filepath.Join(m.config.RuntimeDir, "node_modules/@deepseek-ai/dsh/package.json"))
	var version struct {
		Version string `json:"version"`
	}
	if err != nil || json.Unmarshal(manifest, &version) != nil || version.Version != "0.2.1-rc.3" {
		return "Run make install-dsh to install the pinned version, or set CAMP_DSH_RUNTIME."
	}
	probeDir, err := os.MkdirTemp(m.dir, ".probe-")
	if err != nil {
		return "Cannot prepare the execution isolation environment."
	}
	defer os.RemoveAll(probeDir)
	if err = os.Mkdir(filepath.Join(probeDir, "project"), 0700); err != nil {
		return "Cannot prepare the execution isolation environment."
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err = sandboxAvailable(probeCtx, m.config, probeDir, m.lock); err != nil {
		return "Isolation check failed: execution requires Linux and working Bubblewrap user and process namespaces."
	}
	return ""
}

func (m *Manager) Availability() (bool, string, Budget) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.availableLocked()
}
func (m *Manager) availableLocked() (bool, string, Budget) {
	_, model := m.models.Snapshot()
	budget := Budget{model.MaxTokens, model.MaxSteps, model.TimeoutSeconds}
	if problem := m.admissionProblemLocked(); problem != "" {
		return false, problem, budget
	}
	if model.APIKey == "" {
		return false, "An administrator needs to configure the model connection in Settings.", budget
	}
	return true, "", budget
}

// Queued work uses its frozen private model version, including after the active
// connection has been cleared. Current settings govern only new submissions.
func (m *Manager) admissionProblemLocked() string {
	if m.rechecking {
		return "Execution readiness is being checked."
	}
	if len(m.unsaved) > 0 {
		return "An execution ended but its final record could not be saved. Repair storage and restart Camp before running more work."
	}
	if m.closed {
		return "The service is stopping."
	}
	return m.unavailable
}

func (m *Manager) Send(ctx context.Context, roomID string, input storage.SendMessageInput) (storage.SendResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if result, found, err := m.store.FindSendMessage(ctx, roomID, input); err != nil || found {
		for i := range result.Runs {
			if run, ok := m.unsaved[result.Runs[i].ID]; ok {
				result.Runs[i] = run
			}
		}
		return result, err
	}
	// Post-only discussion stays usable without model credentials. Durable replay
	// is handled by storage before any queue wake, so an HTTP retry cannot execute twice.
	recipients, err := m.store.MessageRecipientIDs(ctx, roomID, input)
	if err != nil {
		return storage.SendResult{}, err
	}
	needsAgent := len(recipients) > 0
	if needsAgent {
		if available, reason, _ := m.availableLocked(); !available {
			return storage.SendResult{}, &storage.Error{Code: "execution_unavailable", Message: reason}
		}
	}
	// Git reads do not hold the scheduler mutex: activity persistence and cancellation
	// for another agent must remain responsive while evidence is checked.
	if hasFileReferences(input.References) {
		m.mu.Unlock()
		err := m.validateReferenceFiles(ctx, roomID, input.References)
		m.mu.Lock()
		if result, found, replayErr := m.store.FindSendMessage(ctx, roomID, input); replayErr != nil || found {
			for i := range result.Runs {
				if run, ok := m.unsaved[result.Runs[i].ID]; ok {
					result.Runs[i] = run
				}
			}
			return result, replayErr
		}
		if err != nil {
			return storage.SendResult{}, err
		}
		if needsAgent {
			if available, reason, _ := m.availableLocked(); !available {
				return storage.SendResult{}, &storage.Error{Code: "execution_unavailable", Message: reason}
			}
		}
	}
	revision, model := m.models.Snapshot()
	result, err := m.store.SendMessage(ctx, roomID, input, storage.Budget{Model: model.Model, RuntimeKey: revision, MaxTokens: model.MaxTokens, MaxSteps: model.MaxSteps, TimeoutSeconds: model.TimeoutSeconds})
	if err == nil {
		m.Wake()
	}
	return result, err
}

func (m *Manager) Get(ctx context.Context, id string) (storage.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if run, ok := m.unsaved[id]; ok {
		return run, nil
	}
	return m.store.GetRun(ctx, id)
}

// ListRoomRuns overlays unsaved terminal snapshots just like Get, so timelines
// cannot display an exited process as running after a final database failure.
func (m *Manager) ListRoomRuns(ctx context.Context, roomID string) ([]storage.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runs, err := m.store.ListRoomRuns(ctx, roomID)
	for i := range runs {
		if run, ok := m.unsaved[runs[i].ID]; ok {
			runs[i] = run
		}
	}
	return runs, err
}

// AgentStates is workspace-wide: one agent may be a member of several rooms,
// but its process and queued work still share one global execution identity.
func (m *Manager) AgentStates(ctx context.Context) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	queued, err := m.store.ListQueuedRuns(ctx)
	if err != nil {
		return nil, err
	}
	states := make(map[string]string)
	for _, run := range queued {
		states[run.AgentID] = "queued"
	}
	for _, e := range m.active {
		states[e.run.AgentID] = e.run.Status
	}
	return states, nil
}

func (m *Manager) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) Cancel(ctx context.Context, id string) (storage.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if run, ok := m.unsaved[id]; ok {
		return run, nil
	}
	if e := m.active[id]; e != nil {
		if e.finishing {
			return e.run, nil
		}
		e.cancelled = true
		err := m.updateLocked(e, func(r *storage.Run) { r.Status = "cancelling" })
		e.cancel()
		return e.run, err
	}
	run, err := m.store.GetRun(ctx, id)
	if err != nil {
		return run, err
	}
	if run.Status == "queued" {
		run.Status = "cancelled"
		run.Error = "Cancelled before execution started."
		return m.finishQueuedLocked(ctx, run)
	}
	return run, nil
}

// A failed queued cancellation must never let the scheduler launch that request.
func (m *Manager) finishQueuedLocked(ctx context.Context, run storage.Run) (storage.Run, error) {
	saved, err := m.store.FinishRun(ctx, run, "", false)
	if err != nil {
		run.Status = "failed"
		run.Error = "The cancellation could not be saved. Execution is suspended until storage is repaired and Camp restarts."
		run.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		m.unsaved[run.ID] = run
		return run, err
	}
	return saved, nil
}

// CancelMatching is used after a membership or enabled-state change. An empty
// roomID cancels this agent's work in every room.
func (m *Manager) CancelMatching(ctx context.Context, roomID, agentID string) error {
	queued, err := m.store.ListQueuedRuns(ctx)
	if err != nil {
		return err
	}
	ids := []string{}
	for _, run := range queued {
		if run.AgentID == agentID && (roomID == "" || run.RoomID == roomID) {
			ids = append(ids, run.ID)
		}
	}
	m.mu.Lock()
	for id, e := range m.active {
		if e.run.AgentID == agentID && (roomID == "" || e.run.RoomID == roomID) {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		if _, cancelErr := m.Cancel(ctx, id); cancelErr != nil {
			err = errors.Join(err, cancelErr)
		}
	}
	return err
}

// WithResourceWriter shares the scheduler lease with explicit edits and restores.
// Callers still compare the expected revision in the final SQLite transaction.
func (m *Manager) WithResourceWriter(ctx context.Context, resourceID string, apply func() error) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return &storage.Error{Code: "execution_unavailable", Message: "The service is stopping."}
	}
	busy := m.manualWriters[resourceID]
	for _, e := range m.active {
		if e.run.ResourceID == resourceID && e.run.AgentSnapshot.Access == "write" {
			busy = true
		}
	}
	if busy {
		m.mu.Unlock()
		return &storage.Error{Code: "resource_busy", Message: "An agent or another person is updating these files. Keep your draft and try again after that work finishes."}
	}
	m.manualWriters[resourceID] = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.manualWriters, resourceID); m.mu.Unlock(); m.Wake() }()
	if err := ctx.Err(); err != nil {
		return err
	}
	return apply()
}

// CancelUser follows an atomic account disable, which prevents new admission.
func (m *Manager) CancelUser(ctx context.Context, userID string) error {
	pending, err := m.store.ListUserActiveRuns(ctx, userID)
	if err != nil {
		return err
	}
	for _, run := range pending {
		if _, cancelErr := m.Cancel(ctx, run.ID); cancelErr != nil {
			err = errors.Join(err, cancelErr)
		}
	}
	return err
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		close(m.stop)
	}
	pending := make([]<-chan struct{}, 0, len(m.active))
	for _, e := range m.active {
		e.interrupted = true
		e.cancel()
		pending = append(pending, e.done)
	}
	m.mu.Unlock()
	for _, done := range pending {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case <-m.stopped:
	case <-ctx.Done():
		return ctx.Err()
	}
	m.closeOnce.Do(func() {
		m.checkMu.Lock()
		defer m.checkMu.Unlock()
		if m.bridge != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			m.layerMu.Lock()
			for id, usage := range m.layerProjects {
				if usage.active != 0 {
					m.closeErr = errors.Join(m.closeErr, errors.New("Disposable LayerFS resources require startup reconciliation"))
					continue
				}
				err := m.bridge.RemoveProject(cleanup, id)
				m.closeErr = errors.Join(m.closeErr, err)
				if err == nil {
					delete(m.layerProjects, id)
				}
			}
			m.layerMu.Unlock()
			cancel()
			m.closeErr = errors.Join(m.closeErr, m.bridge.Close())
		}
		if m.docker != nil {
			m.docker.Close()
		}
		if m.lock != nil {
			_ = m.lock.Close()
		}
	})
	return m.closeErr
}

func (m *Manager) schedule() {
	defer close(m.stopped)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
		case <-m.wake:
		}
		m.admit()
	}
}

func (m *Manager) admit() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.admissionProblemLocked() != "" || len(m.active) >= concurrencyLimit {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	queued, err := m.store.ListQueuedRuns(ctx)
	if err != nil {
		m.logger.Error("Cannot read the execution queue")
		return
	}
	agents, writers := map[string]bool{}, map[string]bool{}
	for id := range m.manualWriters {
		writers[id] = true
	}
	for _, e := range m.active {
		agents[e.run.AgentID] = true
		if e.run.AgentSnapshot.Access == "write" {
			writers[e.run.ResourceID] = true
		}
	}
	// Scan in durable order. A blocked resource does not consume an execution slot,
	// but later work for the same agent must not overtake that agent's first request.
	for _, queuedRun := range queued {
		if queuedRun.CancellationReason != "" {
			queuedRun.Status, queuedRun.Error = "cancelled", queuedRun.CancellationReason
			if _, err := m.finishQueuedLocked(ctx, queuedRun); err != nil {
				return
			}
			continue
		}
		if len(m.active) >= concurrencyLimit {
			break
		}
		if agents[queuedRun.AgentID] {
			continue
		}
		agents[queuedRun.AgentID] = true
		if queuedRun.AgentSnapshot.Access == "write" && writers[queuedRun.ResourceID] {
			continue
		}
		run, err := m.store.ClaimRun(ctx, queuedRun.ID)
		if err != nil {
			var problem *storage.Error
			if errors.As(err, &problem) && problem.Code == "agent_unavailable" {
				queuedRun.Status, queuedRun.Error = "cancelled", problem.Message
				if _, err := m.finishQueuedLocked(ctx, queuedRun); err != nil {
					return
				}
			}
			continue
		}
		timeout := time.Duration(run.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = m.config.Timeout
		}
		runCtx, runCancel := context.WithTimeout(context.Background(), timeout)
		e := &execution{run: run, cancel: runCancel, done: make(chan struct{})}
		m.active[run.ID] = e
		if run.AgentSnapshot.Access == "write" {
			writers[run.ResourceID] = true
		}
		go m.executeRun(runCtx, e)
	}
}

func (m *Manager) updateLocked(e *execution, change func(*storage.Run)) error {
	change(&e.run)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	saved, err := m.store.UpdateRunWithEvent(ctx, e.run, "status", e.run.Status)
	if err == nil {
		e.run = saved
	} else {
		e.writeErr = err
		e.cancel()
	}
	return err
}
func (m *Manager) update(e *execution, change func(*storage.Run)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateLocked(e, change)
}
func (m *Manager) redact(text string) string {
	if m.models != nil {
		text = m.models.Redact(text)
	}
	if m.config.APIKey != "" {
		text = strings.ReplaceAll(text, m.config.APIKey, "[REDACTED]")
	}
	return text
}
func (m *Manager) redactFiles(files []storage.RunFile) {
	for i := range files {
		files[i].Path = m.redact(files[i].Path)
		files[i].Before = m.redact(files[i].Before)
		files[i].After = m.redact(files[i].After)
	}
}
func boundedOutput(old, addition string) string {
	text := strings.ToValidUTF8(old+addition, "�")
	if len(text) <= storage.MaxRunOutputSize {
		return text
	}
	const marker = "[Earlier output was truncated; retention is limited to 256 KiB]\n"
	return marker + strings.ToValidUTF8(text[len(text)-(storage.MaxRunOutputSize-len(marker)-3):], "�")
}
func unfinished(status string) bool {
	return status == "queued" || status == "preparing" || status == "running" || status == "checking" || status == "cancelling"
}
