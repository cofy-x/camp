// Package configuration owns private, versioned operator and administrator settings.
package configuration

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

type Model struct {
	Model          string `json:"model"`
	BaseURL        string `json:"baseUrl"`
	APIKey         string `json:"-"`
	MaxTokens      int    `json:"maxTokens"`
	MaxSteps       int    `json:"maxSteps"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

type Overrides struct{ Environment, EnvFile map[string]string }
type Field struct {
	Source     string `json:"source"`
	Overridden bool   `json:"overridden"`
}
type View struct {
	Model
	APIKeyConfigured bool             `json:"apiKeyConfigured"`
	Revision         string           `json:"revision"`
	Fields           map[string]Field `json:"fields"`
}
type Update struct {
	Model          *string `json:"model,omitempty"`
	BaseURL        *string `json:"baseUrl,omitempty"`
	APIKey         *string `json:"apiKey,omitempty"`
	ClearAPIKey    bool    `json:"clearApiKey,omitempty"`
	MaxTokens      *int    `json:"maxTokens,omitempty"`
	MaxSteps       *int    `json:"maxSteps,omitempty"`
	TimeoutSeconds *int    `json:"timeoutSeconds,omitempty"`
}
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }

type saved struct {
	Revision string            `json:"revision"`
	Managed  map[string]string `json:"managed"`
}
type version struct {
	Revision string `json:"revision"`
	Model    Model  `json:"model"`
	APIKey   string `json:"apiKey"`
}
type Models struct {
	mu        sync.RWMutex
	directory string
	defaults  Model
	overrides Overrides
	saved     saved
	current   Model
	fields    map[string]Field
	versions  map[string]Model
}

var fields = []string{"apiKey", "baseUrl", "model", "maxTokens", "maxSteps", "timeoutSeconds"}

func Defaults() Model {
	return Model{Model: "deepseek-v4-pro", BaseURL: "https://api.deepseek.com/anthropic", MaxTokens: 4096, MaxSteps: 8, TimeoutSeconds: 180}
}

func Open(directory string, defaults Model, overrides Overrides) (*Models, error) {
	for _, name := range []string{directory, filepath.Join(directory, "versions")} {
		if err := os.MkdirAll(name, 0700); err != nil {
			return nil, err
		}
		info, err := os.Lstat(name)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, errors.New("Private settings directories must not be symbolic links")
		}
		if err := os.Chmod(name, 0700); err != nil {
			return nil, err
		}
	}
	m := &Models{directory: directory, defaults: defaults, overrides: overrides, versions: map[string]Model{}}
	m.saved.Managed = map[string]string{}
	if err := readPrivate(filepath.Join(directory, "models.json"), &m.saved); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if m.saved.Managed == nil {
		m.saved.Managed = map[string]string{}
	}
	effective, metadata, err := m.resolve(m.saved.Managed)
	if err != nil {
		return nil, err
	}
	if m.saved.Revision != "" {
		old, err := m.readVersion(m.saved.Revision)
		if err != nil {
			return nil, err
		}
		if reflect.DeepEqual(old, effective) {
			m.current, m.fields = effective, metadata
			return m, nil
		}
	}
	if err := m.commit(effective, metadata, m.saved.Managed); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Models) resolve(managed map[string]string) (Model, map[string]Field, error) {
	values := map[string]string{"apiKey": m.defaults.APIKey, "baseUrl": m.defaults.BaseURL, "model": m.defaults.Model, "maxTokens": strconv.Itoa(m.defaults.MaxTokens), "maxSteps": strconv.Itoa(m.defaults.MaxSteps), "timeoutSeconds": strconv.Itoa(m.defaults.TimeoutSeconds)}
	metadata := map[string]Field{}
	for _, key := range fields {
		metadata[key] = Field{Source: "default"}
		if value, ok := managed[key]; ok {
			values[key] = value
			metadata[key] = Field{Source: "managed"}
		}
		if value := m.overrides.EnvFile[key]; value != "" {
			values[key] = value
			metadata[key] = Field{Source: "env-file", Overridden: true}
		}
		if value := m.overrides.Environment[key]; value != "" {
			values[key] = value
			metadata[key] = Field{Source: "environment", Overridden: true}
		}
	}
	result := Model{APIKey: values["apiKey"], BaseURL: values["baseUrl"], Model: values["model"]}
	result.MaxTokens, _ = strconv.Atoi(values["maxTokens"])
	result.MaxSteps, _ = strconv.Atoi(values["maxSteps"])
	result.TimeoutSeconds, _ = strconv.Atoi(values["timeoutSeconds"])
	return result, metadata, validate(result)
}
func validate(m Model) error {
	for _, value := range []string{m.APIKey, m.BaseURL, m.Model} {
		if !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") || len(value) > 8192 {
			return &Error{"invalid_settings", "Model settings must be bounded, single-line UTF-8 values."}
		}
	}
	if strings.TrimSpace(m.Model) == "" || len(m.Model) > 128 {
		return &Error{"invalid_settings", "Provide a model name of at most 128 bytes."}
	}
	u, err := url.Parse(m.BaseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return &Error{"invalid_settings", "The model URL must be HTTP(S), without credentials, query parameters, or a fragment."}
	}
	if m.MaxTokens < 1 || m.MaxTokens > 8192 || m.MaxSteps < 1 || m.MaxSteps > 32 || m.TimeoutSeconds < 1 || m.TimeoutSeconds > 900 {
		return &Error{"invalid_settings", "Use 1–8192 output tokens, 1–32 observed steps, and a 1–900 second timeout."}
	}
	return nil
}
func (m *Models) Snapshot() (string, Model) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.saved.Revision, m.current
}
func (m *Models) View() View { m.mu.RLock(); defer m.mu.RUnlock(); return m.view() }
func (m *Models) view() View {
	metadata := map[string]Field{}
	for k, v := range m.fields {
		metadata[k] = v
	}
	model := m.current
	model.APIKey = ""
	return View{Model: model, APIKeyConfigured: m.current.APIKey != "", Revision: m.saved.Revision, Fields: metadata}
}
func (m *Models) Update(input Update) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if input.APIKey != nil && input.ClearAPIKey {
		return View{}, &Error{"invalid_settings", "Replace or clear the model key, not both."}
	}
	edits := map[string]string{}
	for key, value := range map[string]*string{"model": input.Model, "baseUrl": input.BaseURL, "apiKey": input.APIKey} {
		if value != nil {
			edits[key] = *value
		}
	}
	for key, value := range map[string]*int{"maxTokens": input.MaxTokens, "maxSteps": input.MaxSteps, "timeoutSeconds": input.TimeoutSeconds} {
		if value != nil {
			edits[key] = strconv.Itoa(*value)
		}
	}
	if input.ClearAPIKey {
		edits["apiKey"] = ""
	}
	if len(edits) == 0 {
		return View{}, &Error{"invalid_settings", "Provide at least one model setting."}
	}
	managed := map[string]string{}
	for k, v := range m.saved.Managed {
		managed[k] = v
	}
	for k, v := range edits {
		if m.fields[k].Overridden {
			return View{}, &Error{"settings_overridden", fmt.Sprintf("%s is controlled by %s. Change that source and restart Camp.", k, m.fields[k].Source)}
		}
		managed[k] = v
	}
	effective, metadata, err := m.resolve(managed)
	if err != nil {
		return View{}, err
	}
	if err := m.commit(effective, metadata, managed); err != nil {
		return View{}, err
	}
	return m.view(), nil
}
func (m *Models) commit(model Model, metadata map[string]Field, managed map[string]string) error {
	revision := m.saved.Revision
	if revision == "" || !reflect.DeepEqual(model, m.current) {
		var bytes [16]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return err
		}
		revision = hex.EncodeToString(bytes[:])
		data, err := json.Marshal(version{Revision: revision, Model: model, APIKey: model.APIKey})
		if err != nil {
			return err
		}
		if err := writePrivate(filepath.Join(m.directory, "versions", revision+".json"), data); err != nil {
			return err
		}
	}
	next := saved{Revision: revision, Managed: managed}
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := writePrivate(filepath.Join(m.directory, "models.json"), data); err != nil {
		return err
	}
	m.saved, m.current, m.fields = next, model, metadata
	m.versions[revision] = model
	return nil
}
func (m *Models) Resolve(revision string) (Model, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readVersion(revision)
}
func (m *Models) readVersion(revision string) (Model, error) {
	if len(revision) != 32 {
		return Model{}, &Error{"configuration_missing", "The private model configuration for this request is unavailable."}
	}
	if _, err := hex.DecodeString(revision); err != nil {
		return Model{}, &Error{"configuration_missing", "The private model configuration for this request is unavailable."}
	}
	if v, ok := m.versions[revision]; ok {
		return v, nil
	}
	var record version
	if err := readPrivate(filepath.Join(m.directory, "versions", revision+".json"), &record); err != nil {
		return Model{}, &Error{"configuration_missing", "The private model configuration for this request is unavailable. Restore its configuration backup or send a new request."}
	}
	record.Model.APIKey = record.APIKey
	if record.Revision != revision || validate(record.Model) != nil {
		return Model{}, errors.New("The private model configuration version is invalid")
	}
	m.versions[revision] = record.Model
	return record.Model, nil
}
func (m *Models) Redact(text string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, model := range m.versions {
		if model.APIKey != "" {
			text = strings.ReplaceAll(text, model.APIKey, "[REDACTED]")
		}
	}
	return text
}
func readPrivate(name string, value any) error {
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return errors.New("Private settings must be regular files within 1 MiB with owner-only permissions")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
func writePrivate(name string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(name), ".settings-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(file.Name(), name); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
