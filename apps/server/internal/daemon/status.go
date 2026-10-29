package daemon

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

type StatusReport struct {
	RunnerID   string             `json:"runnerId"`
	Name       string             `json:"name"`
	Server     string             `json:"server"`
	Executor   string             `json:"executor"`
	Connection string             `json:"connection"`
	Reason     string             `json:"reason,omitempty"`
	ObservedAt string             `json:"observedAt,omitempty"`
	Readiness  *p.ReadinessReport `json:"readiness,omitempty"`
}

func WriteReadiness(directory string, report p.ReadinessReport) error {
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(directory, "readiness.json"), data)
}
func (d *Daemon) recordConnection(connected bool) error {
	state := "disconnected"
	if connected {
		state = "connected"
	}
	d.mu.Lock()
	reason := d.connectionReason
	d.mu.Unlock()
	value := StatusReport{RunnerID: d.config.RunnerID, Name: d.config.Name, Server: d.config.ServerURL, Executor: d.config.Executor, Connection: state, Reason: reason, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(d.config.DataDir, "status.json"), data)
}
func Status(directory string, output io.Writer) error {
	config, err := LoadConfig(directory)
	if err != nil {
		return err
	}
	value := StatusReport{RunnerID: config.RunnerID, Name: config.Name, Server: config.ServerURL, Executor: config.Executor, Connection: "not observed"}
	if data, readErr := os.ReadFile(filepath.Join(directory, "status.json")); readErr == nil {
		var observed StatusReport
		if json.Unmarshal(data, &observed) == nil && observed.RunnerID == config.RunnerID {
			value.Connection, value.ObservedAt, value.Reason = observed.Connection, observed.ObservedAt, observed.Reason
			stamp, parseErr := time.Parse(time.RFC3339Nano, value.ObservedAt)
			if parseErr != nil || time.Since(stamp) > 30*time.Second {
				value.Connection = "not recently observed"
			}
		}
	}
	if data, readErr := os.ReadFile(filepath.Join(directory, "readiness.json")); readErr == nil {
		var report p.ReadinessReport
		if json.Unmarshal(data, &report) == nil {
			value.Readiness = &report
		}
	}
	return json.NewEncoder(output).Encode(value)
}
