package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

type ConfiguredStatus struct {
	WorkspaceBackend string `json:"workspaceBackend"`
	Slots            int    `json:"slots"`
	Sources          int    `json:"sources"`
	ToolRoots        int    `json:"toolRoots"`
}

type ExecutionObservation struct {
	State        string         `json:"state"`
	BootID       string         `json:"bootId,omitempty"`
	Capabilities p.Capabilities `json:"capabilities"`
}

type StatusReport struct {
	Schema               int                   `json:"schema"`
	RunnerID             string                `json:"runnerId"`
	Name                 string                `json:"name"`
	Server               string                `json:"server"`
	Executor             string                `json:"executor"`
	Platform             string                `json:"platform"`
	Configured           ConfiguredStatus      `json:"configured"`
	Connection           string                `json:"connection"`
	Reason               string                `json:"reason,omitempty"`
	ObservedAt           string                `json:"observedAt,omitempty"`
	Observed             *ExecutionObservation `json:"observed,omitempty"`
	Readiness            *p.ReadinessReport    `json:"readiness,omitempty"`
	ReadinessState       string                `json:"readinessState"`
	ReadinessFingerprint string                `json:"readinessFingerprint,omitempty"`
	Storage              *p.StorageHealth      `json:"storage,omitempty"`
}

// These bindings stay private. Configuration hashes include paths and grants,
// never credentials; changing installation or authority invalidates old checks.
type observationBinding struct {
	RunnerID            string `json:"runnerId"`
	StorageID           string `json:"storageId"`
	RegistrationID      string `json:"registrationId"`
	ConfigurationSHA256 string `json:"configurationSHA256"`
	Fingerprint         string `json:"fingerprint,omitempty"`
}

type readinessRecord struct {
	p.ReadinessReport
	Schema  int                 `json:"schema,omitempty"`
	Binding *observationBinding `json:"binding,omitempty"`
}

type connectionRecord struct {
	StatusReport
	Binding *observationBinding `json:"binding,omitempty"`
}

func configuredBackend(config Config) string {
	if config.WorkspaceBackend != "" {
		return config.WorkspaceBackend
	}
	if runtime.GOOS == "darwin" {
		return "native-local"
	}
	if runtime.GOOS == "linux" {
		return "layerfs"
	}
	return "unsupported"
}

func bindObservation(config Config, fingerprint string) observationBinding {
	// Struct encoding is deterministic, including its map fields. Observation
	// timestamps and reported package labels never change execution eligibility.
	settings := struct {
		Executor         string
		WorkspaceBackend string
		BundleDir        string
		DockerSocket     string
		DockerBinary     string
		ToolRoots        []string
		Sources          map[string]string
		Slots            int
		CAFile           string
	}{config.Executor, configuredBackend(config), config.BundleDir, config.DockerSocket, config.DockerBinary, config.ToolRoots, config.Sources, config.Slots, config.CAFile}
	data, _ := json.Marshal(settings)
	sum := sha256.Sum256(data)
	return observationBinding{config.RunnerID, config.StorageID, config.RegistrationID, hex.EncodeToString(sum[:]), fingerprint}
}

func (b *observationBinding) matches(config Config) bool {
	if b == nil {
		return false
	}
	current := bindObservation(config, b.Fingerprint)
	return *b == current
}

// Legacy callers can retain a failed observation before an executor opens.
// Successful readiness without an execution fingerprint is historical evidence.
func WriteReadiness(directory string, report p.ReadinessReport) error {
	config, err := LoadConfig(directory)
	if err == nil {
		return WriteBoundReadiness(config, report, "")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(directory, "readiness.json"), data)
}

func WriteBoundReadiness(config Config, report p.ReadinessReport, fingerprint string) error {
	binding := bindObservation(config, fingerprint)
	data, err := json.Marshal(readinessRecord{ReadinessReport: report, Schema: 1, Binding: &binding})
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(config.DataDir, "readiness.json"), data)
}

func (d *Daemon) recordConnection(connected bool) error {
	state := "disconnected"
	if connected {
		state = "connected"
	}
	d.mu.Lock()
	reason := d.connectionReason
	initializing := d.config.Executor == "native" && d.native == nil && d.failure == nil
	d.mu.Unlock()
	capabilities := d.capabilities()
	if initializing && capabilities.Ready {
		capabilities.Ready = false
		capabilities.Reason = "Native execution is initializing; no current startup check has completed"
	}
	binding := bindObservation(d.config, capabilities.Fingerprint)
	value := connectionRecord{StatusReport: StatusReport{
		Schema: 1, RunnerID: d.config.RunnerID, Name: d.config.Name, Server: d.config.ServerURL,
		Executor: d.config.Executor, Connection: state, Reason: reason,
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Observed:   &ExecutionObservation{State: "current", BootID: d.bootID, Capabilities: capabilities},
	}, Binding: &binding}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(d.config.DataDir, "status.json"), data)
}

func readObservation(name string, target any) error {
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return errors.New("Invalid private Runner observation")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func ReadStatus(directory string) (StatusReport, error) {
	config, err := LoadConfig(directory)
	if err != nil {
		return StatusReport{}, err
	}
	value := StatusReport{
		Schema: 1, RunnerID: config.RunnerID, Name: config.Name, Server: config.ServerURL,
		Executor: config.Executor, Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Configured: ConfiguredStatus{configuredBackend(config), config.Slots, len(config.Sources), len(config.ToolRoots)},
		Connection: "not observed", ReadinessState: "unknown",
	}
	var observed connectionRecord
	if readObservation(filepath.Join(directory, "status.json"), &observed) == nil && observed.RunnerID == config.RunnerID {
		value.ObservedAt = observed.ObservedAt
		value.Observed = observed.Observed
		if value.Observed != nil {
			value.Observed.State = "historical"
		}
		if observed.Binding.matches(config) {
			value.Connection, value.Reason = observed.Connection, observed.Reason
			stamp, parseErr := time.Parse(time.RFC3339Nano, observed.ObservedAt)
			age := time.Since(stamp)
			fresh := parseErr == nil && age >= -5*time.Second && age <= 30*time.Second
			if !fresh {
				value.Connection = "not recently observed"
			}
			if value.Observed != nil {
				value.Observed.State = "stale"
				if fresh {
					value.Observed.State = "current"
				}
			}
		} else if value.Observed == nil {
			// Old status records have no storage/config binding, so cannot attest to
			// the current execution owner even when their timestamp is recent.
			value.Connection = "not recently observed"
		}
	}
	var readiness readinessRecord
	if readObservation(filepath.Join(directory, "readiness.json"), &readiness) == nil {
		value.Readiness, value.ReadinessState = &readiness.ReadinessReport, "historical"
		if readiness.Binding != nil {
			value.ReadinessFingerprint = readiness.Binding.Fingerprint
		}
		if readiness.Binding.matches(config) && (!readiness.Ready || readiness.Binding.Fingerprint != "") {
			value.ReadinessState = "recorded"
			// A matching configured receipt is useful while stopped, but only
			// a recent observation of that execution fingerprint is current.
			if value.Observed != nil && value.Observed.State == "current" {
				if !readiness.Ready || strings.HasSuffix(value.Observed.Capabilities.Fingerprint, ":"+readiness.Binding.Fingerprint) {
					value.ReadinessState = "current"
				} else {
					value.ReadinessState = "historical"
				}
			}
		}
	}
	var storage storedStorageObservation
	if readObservation(filepath.Join(directory, "storage-observation.json"), &storage) == nil && storage.RunnerID == config.RunnerID && storage.StorageID == config.StorageID && p.ValidateStorageHealth(storage.Storage) == nil {
		value.Storage = storage.Storage
	}
	return value, nil
}

// Status retains the machine-readable library boundary. CLI formatting is
// separate and selects this report explicitly with --json.
func Status(directory string, output io.Writer) error {
	value, err := ReadStatus(directory)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(value)
}
