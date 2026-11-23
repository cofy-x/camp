package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestLocalFailureClosesIdleControlSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		var message p.Message
		if err := wsjson.Read(r.Context(), connection, &message); err != nil {
			t.Error(err)
			return
		}
		if err := wsjson.Write(r.Context(), connection, p.Message{Type: "welcome", Version: p.Version, Epoch: 1}); err != nil {
			t.Error(err)
			return
		}
		if err := wsjson.Read(r.Context(), connection, &message); err == nil {
			t.Error("Runner sent a frame instead of closing the idle connection")
		}
	}))
	defer server.Close()
	directory := t.TempDir()
	journal, err := openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.db.Close()
	d := &Daemon{config: Config{DataDir: directory, ServerURL: server.URL, Executor: "fixture", Slots: 1}, client: server.Client(), journal: journal}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	done := make(chan struct{})
	var connectionError error
	go func() {
		defer close(done)
		connectionError = d.connect(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	statusPath := filepath.Join(directory, "status.json")
	var status connectionRecord
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for readObservation(statusPath, &status) != nil || status.Connection != "connected" {
		select {
		case <-ctx.Done():
			t.Fatal("Runner never recorded the welcome")
		case <-ticker.C:
		}
	}
	d.setFailure(errors.New("Local journal unavailable"))
	select {
	case <-done:
		if !errors.Is(connectionError, context.Canceled) {
			t.Fatal("Local failure did not cancel the control session", connectionError)
		}
	case <-ctx.Done():
		t.Fatal("Idle connection waited for the outer context after a local failure")
	}
	if err := readObservation(statusPath, &status); err != nil {
		t.Fatal(err)
	}
	if d.connectionCancel != nil || status.Connection != "disconnected" || status.Observed == nil || status.Observed.Capabilities.Ready {
		t.Fatal("Runner did not release and record its suspended connection", status)
	}
}

func TestIncompatibleProtocolStopsAndPersistsActionableStatus(t *testing.T) {
	for _, fixture := range []struct {
		name     string
		response p.Message
		status   int
		reason   string
	}{
		{"rejected", p.Message{Type: "rejected", Version: p.Version + 1, Reason: "Upgrade required: install the current Camp Runner package."}, 0, "Upgrade required"},
		{"welcome", p.Message{Type: "welcome", Version: p.Version + 1}, 0, "Upgrade required"},
		{"revoked", p.Message{}, http.StatusUnauthorized, "authorization was revoked"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var connections atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connections.Add(1)
				if fixture.status != 0 {
					w.WriteHeader(fixture.status)
					return
				}
				connection, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer connection.CloseNow()
				var hello p.Message
				if err = wsjson.Read(r.Context(), connection, &hello); err != nil {
					t.Error(err)
					return
				}
				if err = wsjson.Write(r.Context(), connection, fixture.response); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			directory := t.TempDir()
			config := Config{DataDir: directory, ServerURL: server.URL, RunnerID: "protocol-runner", Name: "Protocol Runner", Credential: strings.Repeat("a", 64), StorageID: "protocol-storage", Executor: "fixture", Slots: 1}
			if err := saveConfig(config); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := Run(ctx, config, io.Discard)
			if err == nil || !strings.Contains(err.Error(), fixture.reason) {
				t.Fatal("Protocol mismatch did not stop with upgrade instructions", err)
			}
			var permanent interface{ Permanent() bool }
			if !errors.As(err, &permanent) || !permanent.Permanent() {
				t.Fatal("Connection rejection did not prevent service restart", err)
			}
			if connections.Load() != 1 {
				t.Fatal("Incompatible Runner entered a reconnect loop", connections.Load())
			}
			var output bytes.Buffer
			if err := Status(directory, &output); err != nil {
				t.Fatal(err)
			}
			var status StatusReport
			if err := json.Unmarshal(output.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			if status.Connection != "disconnected" || !strings.Contains(status.Reason, fixture.reason) {
				t.Fatal("Persisted status hid the upgrade requirement", status)
			}
		})
	}
}
