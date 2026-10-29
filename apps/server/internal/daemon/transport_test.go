package daemon

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestDaemonTLSReconnectDedupAndLostCompletionResponse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	input := []byte("A stable brief.\n")
	if err := writer.WriteHeader(&tar.Header{Name: "brief.txt", Mode: 0644, Size: int64(len(input)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive.Bytes())
	command := testCommand()
	command.Spec.Access = "write"
	command.Spec.Prompt = "Write a draft for review"
	command.Spec.Project = p.Artifact{URL: "/api/daemon/operations/attempt-1/input?part=project", SHA256: hex.EncodeToString(sum[:]), Bytes: int64(archive.Len())}
	var connections, completed, uploadCount atomic.Int32
	var mu sync.Mutex
	seen := map[int64]p.Event{}
	done := make(chan struct{})
	var doneOnce sync.Once
	credential := ""
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/daemon/enroll" {
			var request p.Enrollment
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				http.Error(w, "bad", 400)
				return
			}
			credential = request.Credential
			_ = json.NewEncoder(w).Encode(p.Joined{RunnerID: "runner-test"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+credential {
			t.Error("missing Runner credential")
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/api/daemon/connect":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.CloseNow()
			number := connections.Add(1)
			var hello p.Message
			if err = wsjson.Read(ctx, conn, &hello); err != nil {
				return
			}
			if hello.Hello == nil || hello.Hello.Version != p.Version {
				t.Error("missing hello")
				return
			}
			if err = wsjson.Write(ctx, conn, p.Message{Type: "welcome", Version: p.Version, Epoch: int64(number)}); err != nil {
				return
			}
			for range 2 {
				if err = wsjson.Write(ctx, conn, p.Message{Type: "command", Command: &command}); err != nil {
					return
				}
			}
			if err = wsjson.Write(ctx, conn, p.Message{Type: "grant", RequestID: hello.RequestID, OperationID: command.OperationID, LeaseMS: 60000, LeaseUntil: time.Now().Add(time.Minute).Format(time.RFC3339Nano)}); err != nil {
				return
			}
			for {
				var message p.Message
				if err = wsjson.Read(ctx, conn, &message); err != nil {
					return
				}
				if message.Type == "accepted" && number == 1 {
					_ = conn.CloseNow()
					return
				}
				if message.Type == "event" && message.Event != nil {
					mu.Lock()
					seen[message.Event.Sequence] = *message.Event
					mu.Unlock()
					if err = wsjson.Write(ctx, conn, p.Message{Type: "event_ack", OperationID: message.Event.OperationID, Sequence: message.Event.Sequence}); err != nil {
						return
					}
				}
			}
		case "/api/daemon/operations/attempt-1/input":
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write(archive.Bytes())
		case "/api/daemon/operations/attempt-1/finalizing":
			if completed.Load() > 0 {
				_ = json.NewEncoder(w).Encode(p.Receipt{Disposition: "committed", Status: "succeeded"})
				doneOnce.Do(func() { close(done) })
				return
			}
			_ = json.NewEncoder(w).Encode(p.Receipt{Disposition: "accepted", LeaseMS: 60000, LeaseUntil: time.Now().Add(time.Minute).Format(time.RFC3339Nano)})
		case "/api/daemon/operations/attempt-1/artifact":
			uploaded, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			hash := sha256.Sum256(uploaded)
			if hex.EncodeToString(hash[:]) != r.Header.Get("X-Camp-SHA256") {
				t.Error("wrong artifact digest")
			}
			uploadCount.Add(1)
			_ = json.NewEncoder(w).Encode(p.Receipt{Disposition: "accepted"})
		case "/api/daemon/operations/attempt-1/complete":
			var result p.Result
			if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
				t.Error(err)
			}
			if result.Status != "succeeded" || result.Archive == nil || !result.CleanupConfirmed {
				t.Errorf("unexpected result: %+v", result)
			}
			if completed.Add(1) == 1 {
				panic(http.ErrAbortHandler)
			}
			_ = json.NewEncoder(w).Encode(p.Receipt{Disposition: "committed", Status: "succeeded"})
			doneOnce.Do(func() { close(done) })
		default:
			t.Errorf("unexpected endpoint %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	directory := t.TempDir()
	ca := filepath.Join(directory, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := Join(ctx, Config{DataDir: directory, ServerURL: server.URL, Name: "test-fixture", CAFile: ca, Executor: "fixture", Slots: 1}, "join-test")
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- Run(ctx, config, io.Discard) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("protocol flow did not finish")
	}
	// Wait for the committed local receipt instead of sleeping past a guessed boundary.
	eventually := time.NewTicker(10 * time.Millisecond)
	defer eventually.Stop()
	for {
		local, err := openJournal(directory)
		if err != nil {
			t.Fatal(err)
		}
		ops, err := local.operations(ctx)
		local.db.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(ops) == 0 {
			break
		}
		select {
		case <-eventually.C:
		case <-ctx.Done():
			t.Fatal("receipt not persisted")
		}
	}
	cancel()
	if err = <-runDone; err != nil {
		t.Fatal(err)
	}
	if connections.Load() < 2 {
		t.Fatal("connection loss was not exercised")
	}
	if completed.Load() != 1 {
		t.Fatalf("completion replay bypassed committed receipt: %d", completed.Load())
	}
	if uploadCount.Load() < 1 {
		t.Fatal("archive was not transferred")
	}
	mu.Lock()
	defer mu.Unlock()
	starts := 0
	running := false
	for _, event := range seen {
		if event.Kind == "fixture" {
			starts++
		}
		if event.Kind == "status" && event.Text == "running" {
			running = true
		}
	}
	if starts != 1 {
		t.Fatalf("duplicate command executed %d times", starts)
	}
	if !running {
		t.Fatal("executor did not report durable running state")
	}
}

func TestUploadQueriesCommittedReceiptBeforeDamagedLocalArchive(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	j, err := openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	command := testCommand()
	if _, err = j.accept(ctx, command); err != nil {
		t.Fatal(err)
	}
	result := p.Result{CompletionID: "done", Status: "succeeded", CleanupConfirmed: true, Archive: &p.Artifact{SHA256: strings.Repeat("a", 64), Bytes: 10}}
	missing := filepath.Join(directory, "result-lost-after-server-commit.tar")
	if err = j.finish(ctx, command.OperationID, result, missing); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if !strings.HasSuffix(r.URL.Path, "/finalizing") {
			t.Error("completed operation tried to transfer local content", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(p.Receipt{Disposition: "committed", Status: "succeeded"})
	}))
	defer server.Close()
	d := &Daemon{config: Config{DataDir: directory, ServerURL: server.URL}, journal: j, client: server.Client(), output: io.Discard, grants: map[string]grant{}}
	d.upload(ctx, operation{Command: command, State: "uploading", Result: &result, ArchivePath: missing})
	ops, err := j.operations(ctx)
	if err != nil || len(ops) != 0 || requests != 1 || !d.capabilities().Ready {
		t.Fatal("local archive loss prevented durable receipt recovery", ops, requests, err)
	}
}

func TestReadOnlyCompletionRevokesModelBeforePublishing(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	j, err := openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	command := testCommand()
	command.Spec.Access = "read"
	if _, err = j.accept(ctx, command); err != nil {
		t.Fatal(err)
	}
	result := p.Result{CompletionID: "done", Status: "succeeded", CleanupConfirmed: true}
	if err = j.finish(ctx, command.OperationID, result, ""); err != nil {
		t.Fatal(err)
	}
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		receipt := p.Receipt{Disposition: "accepted"}
		if strings.HasSuffix(r.URL.Path, "/complete") {
			if len(paths) != 2 || !strings.HasSuffix(paths[0], "/finalizing") {
				t.Error("read-only completion skipped revoking execution authority", paths)
			}
			receipt = p.Receipt{Disposition: "committed", Status: "succeeded"}
		}
		_ = json.NewEncoder(w).Encode(receipt)
	}))
	defer server.Close()
	d := &Daemon{config: Config{DataDir: directory, ServerURL: server.URL}, journal: j, client: server.Client(), output: io.Discard, grants: map[string]grant{}}
	d.upload(ctx, operation{Command: command, State: "uploading", Result: &result})
	if len(paths) != 2 || !strings.HasSuffix(paths[0], "/finalizing") || !strings.HasSuffix(paths[1], "/complete") {
		t.Fatal("read-only result did not follow the execution/upload phase boundary", paths)
	}
}

func TestNextConversationRequestWaitsForDelayedCompletionReceipt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	directory := t.TempDir()
	j, err := openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err = tw.Close(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(archive.Bytes())
	commands := map[string]p.Command{}
	for _, id := range []string{"previous", "next", "unrelated"} {
		command := testCommand()
		command.ID, command.OperationID, command.Spec.AttemptID = id, id, id
		command.Spec.ConversationID, command.Spec.SessionID, command.Spec.Generation = "conversation", "native-session", 1
		command.Spec.Access = "read"
		command.Spec.Project = p.Artifact{URL: "/input/" + id, SHA256: hex.EncodeToString(hash[:]), Bytes: int64(archive.Len())}
		command.Spec.Resume = id == "next"
		if id == "unrelated" {
			command.Spec.ConversationID = "another-conversation"
		}
		commands[id] = command
		if _, err = j.accept(ctx, command); err != nil {
			t.Fatal(err)
		}
	}
	if err = j.finish(ctx, "previous", p.Result{CompletionID: "completed", Status: "succeeded", CleanupConfirmed: true, Resumable: true}, ""); err != nil {
		t.Fatal(err)
	}
	release, completionWaiting := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/input/") {
			if strings.HasSuffix(r.URL.Path, "/next") {
				var state string
				if err := j.db.QueryRow("SELECT state FROM commands WHERE operation_id='previous'").Scan(&state); err != nil || state != "terminal" {
					t.Error("next request started before its predecessor's session receipt", state, err)
				}
			}
			_, _ = w.Write(archive.Bytes())
			return
		}
		receipt := p.Receipt{Disposition: "accepted"}
		if r.URL.Path == "/api/daemon/operations/previous/complete" {
			close(completionWaiting)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			receipt = p.Receipt{Disposition: "committed", Status: "succeeded"}
		}
		_ = json.NewEncoder(w).Encode(receipt)
	}))
	defer server.Close()
	d := &Daemon{config: Config{DataDir: directory, Executor: "fixture", Slots: 2, ServerURL: server.URL}, journal: j, client: server.Client(), output: io.Discard, grants: map[string]grant{"next": {until: time.Now().Add(time.Minute)}, "unrelated": {until: time.Now().Add(time.Minute)}}, active: map[string]*activeOperation{}}
	done := make(chan struct{})
	go func() { d.schedule(ctx); close(done) }()
	defer func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		<-done
		d.workers.Wait()
	}()
	select {
	case <-completionWaiting:
	case <-ctx.Done():
		t.Fatal("previous operation did not reach its delayed completion response")
	}
	waitResult := func(id string) {
		t.Helper()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			ops, err := j.operations(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, op := range ops {
				if op.Command.OperationID == id && op.Result != nil {
					if op.Result.Status != "succeeded" {
						t.Fatal("fixture failed", op.Result)
					}
					return
				}
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatal("operation did not finish", id)
			}
		}
	}
	waitResult("unrelated")
	var state string
	if err = j.db.QueryRow("SELECT state FROM commands WHERE operation_id='next'").Scan(&state); err != nil || state != "accepted" {
		t.Fatal("same-conversation work bypassed the delayed receipt", state, err)
	}
	releaseOnce.Do(func() { close(release) })
	waitResult("next")
}
