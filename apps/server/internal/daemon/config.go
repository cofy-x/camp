// Package daemon owns a Runner's durable transport and local execution lifecycle.
package daemon

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

type Config struct {
	BundleDir         string                `json:"bundleDir,omitempty"`
	RuntimeSource     string                `json:"runtimeSource,omitempty"`
	DockerSocket      string                `json:"dockerSocket,omitempty"`
	DockerBinary      string                `json:"dockerBinary,omitempty"`
	WorkspaceBackend  string                `json:"workspaceBackend,omitempty"`
	ServerURL         string                `json:"serverUrl"`
	RunnerID          string                `json:"runnerId"`
	Credential        string                `json:"credential"`
	StorageID         string                `json:"storageId"`
	RegistrationID    string                `json:"registrationId"`
	Name              string                `json:"name"`
	CAFile            string                `json:"caFile,omitempty"`
	CAFileSet         bool                  `json:"-"`
	Executor          string                `json:"executor"`
	Slots             int                   `json:"slots"`
	Sources           map[string]string     `json:"sources,omitempty"`
	ToolRoots         []string              `json:"toolRoots,omitempty"`
	PendingToken      string                `json:"pendingToken,omitempty"`
	DataDir           string                `json:"-"`
	Native            nativeexecutor.Config `json:"-"`
	NativeUnavailable string                `json:"-"`
}

func validateSources(sources map[string]string) error {
	if len(sources) > p.MaxSources {
		return fmt.Errorf("A Runner supports at most %d source aliases", p.MaxSources)
	}
	for id := range sources {
		if !p.ValidSourceID(id) {
			return errors.New("Source aliases must start with a lowercase letter and contain at most 48 lowercase letters, digits, hyphens or underscores")
		}
	}
	return nil
}

func randomID() string {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value)
}

func LoadConfig(directory string) (Config, error) {
	data, err := os.ReadFile(filepath.Join(directory, "daemon.json"))
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err = json.Unmarshal(data, &config); err != nil {
		return config, err
	}
	if !validRuntimeSource(config.RuntimeSource) {
		return config, errors.New("Runtime source must be github or server")
	}
	if err := nativeexecutor.ValidateWorkspaceBackend(config.WorkspaceBackend); err != nil {
		return config, err
	}
	config.DataDir, err = filepath.Abs(directory)
	return config, err
}

func saveConfig(config Config) error {
	if err := os.MkdirAll(config.DataDir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(config.DataDir, "daemon.json"), data)
}

func writePrivate(name string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(name), ".pending-")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = errors.Join(f.Sync(), f.Close()); err != nil {
		return err
	}
	if err = os.Rename(temporary, name); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func endpoint(server, reference string) (string, error) {
	base, err := url.Parse(server)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return "", errors.New("Server URL must contain only the trusted HTTPS origin")
	}
	if base.Scheme != "https" {
		host := base.Hostname()
		ip := net.ParseIP(host)
		if base.Scheme != "http" || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return "", errors.New("Runner connections require HTTPS except on localhost")
		}
	}
	ref, err := url.Parse(reference)
	if err != nil {
		return "", err
	}
	target := base.ResolveReference(ref)
	if target.Scheme != base.Scheme || target.Host != base.Host || target.User != nil || target.Fragment != "" {
		return "", errors.New("An operation URL must use the enrolled Camp origin")
	}
	return target.String(), nil
}

func httpClient(config Config) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if config.CAFile != "" {
		data, err := os.ReadFile(config.CAFile)
		if err != nil {
			return nil, err
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, errors.New("CA file contains no valid certificates")
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	transport.ResponseHeaderTimeout = 20 * time.Second
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("Runner requests do not follow redirects")
	}}, nil
}

// Join persists registration identity before sending it, so a lost enrollment
// response can recover the same Runner without spending another join token.
func Join(ctx context.Context, config Config, token string) (Config, error) {
	if !validRuntimeSource(config.RuntimeSource) {
		return config, errors.New("Runtime source must be github or server")
	}
	if err := validateSources(config.Sources); err != nil {
		return config, err
	}
	if _, err := endpoint(config.ServerURL, "/api/daemon/enroll"); err != nil {
		return config, err
	}
	var err error
	config.DataDir, err = filepath.Abs(config.DataDir)
	if err != nil {
		return config, err
	}
	if err = os.MkdirAll(config.DataDir, 0700); err != nil {
		return config, err
	}
	// Registration and running share the same private storage ownership. Two
	// simultaneous joins must not replace each other's already-sent credentials.
	lock, err := execution.AcquireLock(config.DataDir)
	if err != nil {
		return config, err
	}
	if lock != nil {
		defer lock.Close()
	}
	if previous, loadErr := LoadConfig(config.DataDir); loadErr == nil {
		if err = validateSources(previous.Sources); err != nil {
			return config, err
		}
		if previous.ServerURL != config.ServerURL {
			return config, errors.New("This private directory belongs to another Camp server")
		}
		if config.CAFileSet || config.CAFile != "" {
			previous.CAFile = config.CAFile
			if err := saveConfig(previous); err != nil {
				return previous, err
			}
		}
		if previous.RunnerID != "" {
			return previous, nil
		}
		config = previous
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return config, loadErr
	}
	if config.Credential == "" {
		config.Credential = randomID()
		config.StorageID = randomID()
		config.RegistrationID = randomID()
		config.PendingToken = strings.TrimSpace(token)
	}
	if config.PendingToken == "" {
		return config, errors.New("A join token is required")
	}
	if config.Executor == "" {
		config.Executor = "native"
	}
	if config.Executor != "native" && config.Executor != "fixture" {
		return config, errors.New("Executor must be native or fixture")
	}
	if config.Slots == 0 {
		config.Slots = 1
	}
	if config.Slots < 1 || config.Slots > p.MaxSlots {
		return config, fmt.Errorf("Runner slots must be between 1 and %d", p.MaxSlots)
	}
	if err = saveConfig(config); err != nil {
		return config, err
	}
	client, err := httpClient(config)
	if err != nil {
		return config, err
	}
	defer client.CloseIdleConnections()
	var joined p.Joined
	err = requestJSON(ctx, client, config, "POST", "/api/daemon/enroll", p.Enrollment{Token: config.PendingToken, RegistrationID: config.RegistrationID, Credential: config.Credential, Name: config.Name, StorageID: config.StorageID}, &joined)
	if err != nil {
		return config, err
	}
	if joined.RunnerID == "" {
		return config, errors.New("Server did not return a Runner identity")
	}
	config.RunnerID = joined.RunnerID
	config.PendingToken = ""
	return config, saveConfig(config)
}

func requestJSON(ctx context.Context, client *http.Client, config Config, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(data))
	}
	target, err := endpoint(config.ServerURL, path)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+config.Credential)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &httpError{Status: response.StatusCode}
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output)
	}
	return nil
}

type httpError struct{ Status int }

func (e *httpError) Error() string { return fmt.Sprintf("Camp request failed (HTTP %d)", e.Status) }

// SaveInstallation updates only runtime locations after an explicit setup.
func SaveInstallation(config Config, bundle, docker string) error {
	if !validRuntimeSource(config.RuntimeSource) {
		return errors.New("Runtime source must be github or server")
	}
	lock, err := execution.AcquireLock(config.DataDir)
	if err != nil {
		return err
	}
	if lock != nil {
		defer lock.Close()
	}
	current, err := LoadConfig(config.DataDir)
	if err != nil {
		return err
	}
	if current.RunnerID != config.RunnerID {
		return errors.New("Runner identity changed during setup")
	}
	current.BundleDir = bundle
	current.DockerSocket = docker
	current.DockerBinary = config.DockerBinary
	current.RuntimeSource = config.RuntimeSource
	return saveConfig(current)
}

func validRuntimeSource(value string) bool {
	return value == "" || value == "github" || value == "server"
}

// SaveWorkspaceBackend is a stopped-Runner installation change. Enrollment,
// sources, authorization grants, journals and retained captures remain intact.
func SaveWorkspaceBackend(config Config, backend string) error {
	if err := nativeexecutor.ValidateWorkspaceBackend(backend); err != nil {
		return err
	}
	lock, err := execution.AcquireLock(config.DataDir)
	if err != nil {
		return err
	}
	if lock != nil {
		defer lock.Close()
	}
	current, err := LoadConfig(config.DataDir)
	if err != nil {
		return err
	}
	if current.RunnerID != config.RunnerID || current.StorageID != config.StorageID || current.RegistrationID != config.RegistrationID {
		return errors.New("Runner identity changed while selecting the workspace backend")
	}
	if current.Executor != "native" {
		return errors.New("Workspace selection requires the native executor")
	}
	current.WorkspaceBackend = backend
	return saveConfig(current)
}

// SaveToolRoots changes explicit local grants while retaining enrollment and
// journal identity. A running Runner must be stopped before changing authority.
func SaveToolRoots(config Config, roots []string) error {
	lock, err := execution.AcquireLock(config.DataDir)
	if err != nil {
		return err
	}
	if lock != nil {
		defer lock.Close()
	}
	current, err := LoadConfig(config.DataDir)
	if err != nil {
		return err
	}
	if current.RunnerID != config.RunnerID || current.StorageID != config.StorageID || current.RegistrationID != config.RegistrationID {
		return errors.New("Runner identity changed while updating tool grants")
	}
	current.ToolRoots, err = nativeexecutor.ResolveToolRoots(current.DataDir, roots)
	if err != nil {
		return err
	}
	return saveConfig(current)
}
