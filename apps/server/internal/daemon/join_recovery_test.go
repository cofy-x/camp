package daemon

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestJoinLostResponseReusesRegistrationAndJoinedRestartNeedsNoNetwork(t *testing.T) {
	var mu sync.Mutex
	var requests []p.Enrollment
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input p.Enrollment
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		requests = append(requests, input)
		first := len(requests) == 1
		mu.Unlock()
		if first {
			// The server committed registration but its response was lost.
			panic(http.ErrAbortHandler)
		}
		_ = json.NewEncoder(w).Encode(p.Joined{RunnerID: "registered-runner"})
	}))
	defer server.Close()
	directory := t.TempDir()
	ca := filepath.Join(directory, "test-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	config := Config{DataDir: directory, ServerURL: server.URL, Name: "Recovery", Executor: "fixture", Slots: 1, CAFile: ca}
	if _, err := Join(context.Background(), config, "one-use-token"); err == nil {
		t.Fatal("a lost enrollment response was reported as complete")
	}
	pending, err := LoadConfig(directory)
	if err != nil || pending.RunnerID != "" || pending.PendingToken == "" || pending.Credential == "" || pending.RegistrationID == "" {
		t.Fatalf("pending identity not persisted: %v", err)
	}
	joined, err := Join(context.Background(), config, "")
	if err != nil || joined.RunnerID != "registered-runner" || joined.PendingToken != "" {
		t.Fatalf("registration recovery failed: %v", err)
	}
	mu.Lock()
	identical := len(requests) == 2 && reflect.DeepEqual(requests[0], requests[1])
	mu.Unlock()
	if !identical {
		t.Fatal("retry changed the previously committed enrollment identity")
	}
	server.Close()
	replay, err := Join(context.Background(), config, "")
	if err != nil || replay.RunnerID != joined.RunnerID || replay.Credential != joined.Credential {
		t.Fatalf("an enrolled Runner attempted network registration again: %v", err)
	}
}

func TestConcurrentJoinCannotReplacePendingCredential(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Runner storage ownership is enforced by the Linux daemon")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan p.Enrollment, 2)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input p.Enrollment
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		entered <- input
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(p.Joined{RunnerID: "registered-runner"})
	}))
	defer server.Close()
	config := Config{DataDir: t.TempDir(), ServerURL: server.URL, Name: "Concurrent", Executor: "fixture", Slots: 1}
	done := make(chan error, 1)
	go func() { _, err := Join(ctx, config, "one-use-token"); done <- err }()
	var first p.Enrollment
	select {
	case first = <-entered:
	case <-ctx.Done():
		t.Fatal("first enrollment did not reach its persisted-request barrier")
	}
	_, secondErr := Join(ctx, config, "one-use-token")
	if secondErr == nil {
		t.Error("a simultaneous enrollment acquired the same private directory")
	}
	pending, err := LoadConfig(config.DataDir)
	if err != nil || pending.Credential != first.Credential || pending.RegistrationID != first.RegistrationID {
		t.Errorf("a concurrent join replaced the pending identity: %v", err)
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	joined, err := LoadConfig(config.DataDir)
	if err != nil || joined.RunnerID != "registered-runner" || joined.Credential != first.Credential {
		t.Fatalf("the committed registration no longer matches local storage: %v", err)
	}
}

func TestJoinCanCorrectCAWithoutReplacingPendingIdentity(t *testing.T) {
	received := make(chan p.Enrollment, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input p.Enrollment
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		received <- input
		_ = json.NewEncoder(w).Encode(p.Joined{RunnerID: "corrected-ca-runner"})
	}))
	defer server.Close()
	directory := t.TempDir()
	wrong := filepath.Join(directory, "wrong.pem")
	if err := os.WriteFile(wrong, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	config := Config{DataDir: directory, ServerURL: server.URL, Name: "Private CA", Executor: "fixture", Slots: 1, CAFile: wrong}
	if _, err := Join(context.Background(), config, "single-code"); err == nil {
		t.Fatal("Wrong CA unexpectedly joined")
	}
	pending, err := LoadConfig(directory)
	if err != nil || pending.RegistrationID == "" {
		t.Fatal("Pending registration was not saved", err)
	}
	correct := filepath.Join(directory, "correct.pem")
	if err := os.WriteFile(correct, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	config.CAFile = correct
	joined, err := Join(context.Background(), config, "")
	if err != nil {
		t.Fatal("Corrected CA could not recover registration", err)
	}
	if joined.RegistrationID != pending.RegistrationID || joined.Credential != pending.Credential || joined.StorageID != pending.StorageID || joined.CAFile != correct {
		t.Fatal("CA correction replaced identity")
	}
	if len(received) != 1 {
		t.Fatal("CA correction issued a new registration")
	}
	enrollment := <-received
	if enrollment.Token != "single-code" || enrollment.RegistrationID != pending.RegistrationID {
		t.Fatal("CA correction issued a new registration")
	}
	config.CAFile = ""
	config.CAFileSet = true
	cleared, err := Join(context.Background(), config, "")
	if err != nil || cleared.CAFile != "" || cleared.RunnerID != joined.RunnerID {
		t.Fatal("Explicitly restoring system CA trust changed identity", err)
	}
}
