package configuration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Diagnostic history contains controlled operational metadata, never requests,
// model responses, credentials, or document content.
type Diagnostic struct {
	Status        string `json:"status"`
	Detail        string `json:"detail"`
	CheckedAt     string `json:"checkedAt"`
	LastSuccessAt string `json:"lastSuccessAt,omitempty"`
	LastSuccess   string `json:"lastSuccess,omitempty"`
	LastFailureAt string `json:"lastFailureAt,omitempty"`
	LastFailure   string `json:"lastFailure,omitempty"`
}
type Diagnostics struct {
	mu      sync.RWMutex
	path    string
	records map[string]Diagnostic
}

// Connection observations are revision-specific, but an operator can rotate
// settings indefinitely. Bound this diagnostic cache independently of private
// configuration versions, which queued requests may still need.
const maxConnectionDiagnostics = 64

func OpenDiagnostics(directory string) (*Diagnostics, error) {
	result := &Diagnostics{path: filepath.Join(directory, "readiness.json"), records: map[string]Diagnostic{}}
	if err := readPrivate(result.path, &result.records); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if result.records == nil {
		result.records = map[string]Diagnostic{}
	}
	return result, nil
}
func (d *Diagnostics) Get(id string) Diagnostic {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.records[id]
}
func (d *Diagnostics) Record(id, status, detail string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(detail) > 2048 {
		detail = detail[:2048]
	}
	record := d.records[id]
	record.Status, record.Detail, record.CheckedAt = status, detail, time.Now().UTC().Format(time.RFC3339Nano)
	if status == "ready" {
		record.LastSuccessAt, record.LastSuccess = record.CheckedAt, detail
	} else if status == "missing" || status == "error" {
		record.LastFailureAt, record.LastFailure = record.CheckedAt, detail
	}
	next := make(map[string]Diagnostic, len(d.records)+1)
	for key, value := range d.records {
		next[key] = value
	}
	next[id] = record
	connections := make([]string, 0)
	for key := range next {
		if strings.HasPrefix(key, "connection:") {
			connections = append(connections, key)
		}
	}
	slices.SortFunc(connections, func(left, right string) int {
		return strings.Compare(next[right].CheckedAt, next[left].CheckedAt)
	})
	if len(connections) > maxConnectionDiagnostics {
		for _, key := range connections[maxConnectionDiagnostics:] {
			delete(next, key)
		}
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := writePrivate(d.path, encoded); err != nil {
		return err
	}
	d.records = next
	return nil
}
