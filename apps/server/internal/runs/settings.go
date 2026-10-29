package runs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/documenttext"
	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type ReadinessComponent struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	configuration.Diagnostic
}
type Readiness struct {
	Ready      bool                 `json:"ready"`
	CheckedAt  string               `json:"checkedAt"`
	Components []ReadinessComponent `json:"components"`
}
type EnvironmentSettings struct {
	Platform       string         `json:"platform"`
	NodeVersion    string         `json:"nodeVersion"`
	PackageManager string         `json:"packageManager"`
	CommandNetwork bool           `json:"commandNetwork"`
	Concurrency    int            `json:"concurrency"`
	Checks         string         `json:"checks"`
	Limits         map[string]int `json:"limits"`
}
type StorageSettings struct {
	DataDirectory string `json:"dataDirectory"`
	Files         string `json:"files"`
	Versions      string `json:"versions"`
	Backups       string `json:"backups"`
}

func (m *Manager) ModelSettings() configuration.View { return m.models.View() }
func (m *Manager) UpdateModelSettings(ctx context.Context, input configuration.Update) (configuration.View, error) {
	revision, _ := m.models.Snapshot()
	if err := m.store.RecordManagementEvent(ctx, "model.update.requested", revision, "Administrator requested a model configuration change."); err != nil {
		return configuration.View{}, err
	}
	value, err := m.models.Update(input)
	if err == nil {
		m.Wake()
		if auditErr := m.store.RecordManagementEvent(ctx, "model.updated", value.Revision, "Private model configuration version saved."); auditErr != nil {
			return value, &configuration.Error{Code: "settings_audit_failed", Message: "The model configuration changed, but its completion audit could not be saved. Restore database availability before further changes."}
		}
	}
	return value, err
}
func (m *Manager) EnvironmentSettings() EnvironmentSettings {
	return EnvironmentSettings{Platform: "Linux x86_64 / Docker + LayerFS", NodeVersion: projectenv.NodeVersion, PackageManager: projectenv.PackageManager, CommandNetwork: false, Concurrency: concurrencyLimit, Checks: "Resource-specific command or Node checks. No automatic language toolchain installation.", Limits: map[string]int{"memoryMiB": 1024, "processes": 256, "cpus": 2, "commandOutputMiB": 8}}
}
func (m *Manager) StorageSettings() StorageSettings {
	return StorageSettings{DataDirectory: filepath.Dir(m.dir), Files: "Room and Library files use managed resources. Server-path import requires explicit operator roots.", Versions: "Published files and retained results use immutable Git objects; disposable execution workspaces use LayerFS.", Backups: "Back up the data directory, including private settings and versions. Stop Camp for a consistent filesystem backup."}
}

func (m *Manager) CheckReadiness(ctx context.Context, recheck bool) (Readiness, error) {
	m.checkMu.Lock()
	defer m.checkMu.Unlock()
	if recheck {
		m.mu.Lock()
		if m.closed || len(m.active) > 0 {
			m.mu.Unlock()
			return Readiness{}, &configuration.Error{Code: "runtime_busy", Message: "Wait for active agent work to finish before rechecking execution."}
		}
		m.rechecking = true
		m.mu.Unlock()
		defer func() { m.mu.Lock(); m.rechecking = false; m.mu.Unlock(); m.Wake() }()
		if err := m.refreshReadiness(ctx); err != nil {
			return Readiness{}, err
		}
		m.mu.Lock()
		m.rechecking = false
		m.mu.Unlock()
	}
	result := Readiness{Components: []ReadinessComponent{}}
	revision, model := m.models.Snapshot()
	configured := ReadinessComponent{ID: "model", Label: "Model configuration", Diagnostic: configuration.Diagnostic{Status: "ready", Detail: "Configured. Provider connectivity is verified only by an explicit agent request."}}
	if model.APIKey == "" {
		configured.Status = "missing"
		configured.Detail = "An administrator needs to configure the model key."
	}
	result.Components = append(result.Components, configured)
	for _, component := range []struct{ id, label, key, initial string }{
		{"connection", "Model connection", "connection:" + revision, "Not verified for this configuration. Send an explicit agent request to observe a real provider response."},
		{"git", "File versions", "git", "Recheck to verify Git availability."},
		{"node", "Host Node", "node", "Recheck to verify the configured Node executable."},
		{"dsh", "DSH runtime", "dsh", "Recheck to verify the pinned DSH runtime."},
		{"helper", "Sandbox supervisor", "helper", "Recheck to verify the trusted helper."},
		{"layerfs", "LayerFS and Docker", "layerfs", "Recheck to verify the private execution runtime."},
		{"isolation", "Model process isolation", "isolation", "Recheck to exercise Bubblewrap isolation."},
		{"storage", "SQLite", "storage", "Recheck to verify database availability."},
		{"document-pdf", "PDF text preparation", "document-pdf", "Recheck to exercise the isolated PDF reader. Original documents can still be downloaded."},
		{"document-docx", "DOCX text preparation", "document-docx", "Recheck to exercise the isolated DOCX reader. Original documents can still be downloaded."},
		{"execution", "Last agent execution", "execution", "No agent execution has completed yet."},
	} {
		diagnostic := m.diagnostics.Get(component.key)
		if diagnostic.Status == "" {
			diagnostic.Status = "unknown"
			diagnostic.Detail = component.initial
		}
		if diagnostic.CheckedAt > result.CheckedAt {
			result.CheckedAt = diagnostic.CheckedAt
		}
		result.Components = append(result.Components, ReadinessComponent{ID: component.id, Label: component.label, Diagnostic: diagnostic})
	}
	ready, reason, _ := m.Availability()
	result.Ready = ready
	runtime := configuration.Diagnostic{Status: "ready", Detail: "Execution admission is available. Configured, observed connection, and prior outcomes are shown separately."}
	if !ready {
		runtime.Status = "missing"
		runtime.Detail = reason
	}
	result.Components = append(result.Components, ReadinessComponent{ID: "runtime", Label: "Execution readiness", Diagnostic: runtime})
	return result, nil
}

func (m *Manager) refreshReadiness(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var persistenceErr error
	record := func(id, success string, err error) {
		status, detail := "ready", success
		if err != nil {
			status = "missing"
			detail = m.redact(err.Error())
		}
		persistenceErr = errors.Join(persistenceErr, m.diagnostics.Record(id, status, detail))
	}
	// Reinitialize only while idle. This reconciles uncertain previous allocations
	// without touching durable resource versions or retrying model requests.
	if m.bridge != nil {
		_ = m.bridge.Close()
		m.bridge = nil
	}
	if m.docker != nil {
		m.docker.Close()
		m.docker = nil
	}
	layerProblem := m.openLayerFS(ctx)
	var layerErr error
	if layerProblem != "" {
		layerErr = errors.New(layerProblem)
	}
	record("layerfs", "Private LayerFS and Docker runtime initialized; stale owned execution resources reconciled.", layerErr)
	executionProblem := m.checkAvailability(ctx)
	if executionProblem == "" {
		executionProblem = layerProblem
	}
	m.mu.Lock()
	m.unavailable = executionProblem
	m.mu.Unlock()
	_, gitErr := exec.LookPath("git")
	record("git", "Git is installed.", gitErr)
	_, nodeErr := exec.LookPath(m.config.NodePath)
	record("node", "Configured host Node executable is available.", nodeErr)
	data, runtimeErr := os.ReadFile(filepath.Join(m.config.RuntimeDir, "node_modules/@deepseek-ai/dsh/package.json"))
	var manifest struct {
		Version string `json:"version"`
	}
	if runtimeErr == nil && (json.Unmarshal(data, &manifest) != nil || manifest.Version != "0.2.1-rc.3") {
		runtimeErr = errors.New("Install the pinned DSH runtime with make install-dsh.")
	}
	record("dsh", "Pinned DSH 0.2.1-rc.3 is installed.", runtimeErr)
	info, helperErr := os.Stat(m.config.SandboxHelper)
	if helperErr == nil && (!info.Mode().IsRegular() || info.Mode()&0111 == 0) {
		helperErr = errors.New("The trusted sandbox helper must be executable.")
	}
	record("helper", "Trusted sandbox helper is installed.", helperErr)
	probeDir, probeErr := os.MkdirTemp(m.dir, ".probe-")
	if probeErr == nil {
		defer os.RemoveAll(probeDir)
		probeErr = os.Mkdir(filepath.Join(probeDir, "project"), 0700)
		if probeErr == nil {
			probe, cancel := context.WithTimeout(ctx, 5*time.Second)
			probeErr = sandboxAvailable(probe, m.config, probeDir, m.lock)
			cancel()
		}
	}
	record("isolation", "Bubblewrap isolation probe passed.", probeErr)
	record("storage", "SQLite database responded.", m.store.Ping(ctx))
	program, _ := os.Executable()
	reader := documenttext.New(documenttext.Config{ExecutablePath: program, RuntimeDir: m.config.DocumentRuntimeDir, NodePath: m.config.NodePath})
	for _, format := range []string{"pdf", "docx"} {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := reader.Probe(probe, format)
		cancel()
		record("document-"+format, "The isolated reader extracted the expected text from a generated fixture.", err)
	}
	return persistenceErr
}

func (m *Manager) recordModelDiagnostic(revision string, e *execution, modelErr, contextErr error) {
	status, detail := "", ""
	if e.modelResponded > 0 {
		status = "ready"
		detail = "The provider returned a usable response during an explicit agent request."
	}
	if e.modelRequested > e.modelResponded && modelErr != nil && contextErr == nil {
		status = "error"
		detail = "An explicit model request ended without a usable response. Inspect its retained run activity."
	}
	if status != "" {
		if err := m.diagnostics.Record("connection:"+revision, status, detail); err != nil {
			m.logger.Error("Could not persist model connection diagnostic")
		}
	}
}
func (m *Manager) recordExecutionDiagnostic(run storage.Run) {
	status, detail := "ready", "Agent execution completed. Consult its recorded checks and file publication result."
	if run.Status != "succeeded" {
		status = "error"
		detail = "Agent execution ended with status " + run.Status + ". Inspect the retained run activity and files."
	}
	if err := m.diagnostics.Record("execution", status, detail); err != nil {
		m.logger.Error("Could not persist execution diagnostic")
	}
}
