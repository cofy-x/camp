package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

type activeOperation struct {
	cancel    context.CancelFunc
	deadline  time.Time
	cancelled bool
}
type grant struct{ until time.Time }

type Daemon struct {
	config           Config
	client           *http.Client
	journal          *journal
	native           *nativeexecutor.Executor
	readiness        *p.ReadinessReport
	output           io.Writer
	mu               sync.Mutex
	active           map[string]*activeOperation
	grants           map[string]grant
	failure          error
	bootID           string
	grantRequests    map[string]time.Time
	connectionCancel context.CancelFunc
	connectionReason string
	workers          sync.WaitGroup
}

type connectionRefusedError struct{ reason string }

func (e *connectionRefusedError) Error() string   { return e.reason }
func (e *connectionRefusedError) Permanent() bool { return true }

func Run(ctx context.Context, config Config, output io.Writer) error {
	if err := validateSources(config.Sources); err != nil {
		return err
	}
	if config.RunnerID == "" || config.Credential == "" || config.StorageID == "" {
		return errors.New("Connect to Camp with camp runner setup before running the Runner")
	}
	if config.Executor != "native" && config.Executor != "fixture" {
		return errors.New("Executor must be native or fixture")
	}
	if config.Slots < 1 || config.Slots > p.MaxSlots {
		return fmt.Errorf("Runner slots must be between 1 and %d", p.MaxSlots)
	}
	var err error
	config.DataDir, err = filepath.Abs(config.DataDir)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(config.DataDir, 0700); err != nil {
		return err
	}
	config.DataDir, err = filepath.EvalSymlinks(config.DataDir)
	if err != nil {
		return err
	}
	lock, err := execution.AcquireLock(config.DataDir)
	if err != nil {
		return err
	}
	if lock != nil {
		defer lock.Close()
	}
	client, err := httpClient(config)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	journal, err := openJournal(config.DataDir)
	if err != nil {
		return err
	}
	defer journal.db.Close()
	d := &Daemon{config: config, client: client, journal: journal, output: output, active: map[string]*activeOperation{}, grants: map[string]grant{}, bootID: randomID()}
	cleanupConfirmed := true
	if config.Executor == "native" {
		config.Native.DataDir = config.DataDir
		config.Native.Lock = lock
		config.Native.CAFile = config.CAFile
		initCtx, stop := context.WithTimeout(ctx, 90*time.Second)
		if config.NativeUnavailable != "" {
			err = errors.New(config.NativeUnavailable)
		} else {
			d.native, err = nativeexecutor.Open(initCtx, config.Native)
		}
		report := nativeexecutor.HostChecks(config.Native)
		if err == nil {
			report = d.native.SelfTest(initCtx)
			if !report.Ready {
				err = errors.New("The execution self-test failed; run camp runner status for details")
			}
		} else {
			report.Checks = append(report.Checks, p.ReadinessCheck{ID: "runtime", Label: "Bundled execution runtime", Status: "failed", Detail: err.Error()})
		}
		if err != nil {
			report.Ready = false
		}
		d.readiness = &report
		if saveErr := WriteReadiness(config.DataDir, report); saveErr != nil {
			err = errors.Join(err, saveErr)
		}
		if d.native != nil {
			defer d.native.Close()
		}
		stop()
		if err != nil {
			d.failure = err
			cleanupConfirmed = false
		}
	}
	if err := d.recordConnection(false); err != nil {
		return err
	}
	defer d.recordConnection(false)
	// Only complete, fsynced manifests can resume upload. An interrupted model
	// process is never restarted, even when it may have made no provider request.
	previous, err := journal.operations(ctx)
	if err != nil {
		return err
	}
	for _, op := range previous {
		if op.State == "running" {
			result := p.Result{CompletionID: randomID(), Status: "interrupted", Error: "Runner restarted before execution completed. The request was not replayed.", Checks: []p.CheckResult{}, CleanupConfirmed: cleanupConfirmed}
			if err = journal.finish(ctx, op.Command.OperationID, result, ""); err != nil {
				return err
			}
		}
	}
	if cleanupConfirmed {
		if err = d.pruneTerminalScratch(ctx); err != nil {
			return err
		}
	}
	workerCtx, stopWorkers := context.WithCancel(ctx)
	schedulerDone := make(chan struct{})
	go func() { defer close(schedulerDone); d.schedule(workerCtx) }()
	defer func() {
		stopWorkers()
		<-schedulerDone
		d.mu.Lock()
		for _, active := range d.active {
			active.cancel()
		}
		d.mu.Unlock()
		d.workers.Wait()
	}()
	fmt.Fprintln(output, "Camp Runner running as", config.Name, "("+config.RunnerID+").")
	backoff := time.Second
	for ctx.Err() == nil {
		err = d.connect(ctx)
		if ctx.Err() != nil {
			break
		}
		var httpErr *httpError
		if errors.As(err, &httpErr) && (httpErr.Status == 401 || httpErr.Status == 403) {
			err = &connectionRefusedError{reason: "Runner authorization was revoked or disabled"}
		}
		var refused *connectionRefusedError
		if errors.As(err, &refused) {
			d.mu.Lock()
			d.connectionReason = refused.Error()
			d.mu.Unlock()
			d.cancelAll()
			return refused
		}
		fmt.Fprintln(output, "Camp connection interrupted; reconnecting with the same durable operations.")
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		if backoff < 10*time.Second {
			backoff *= 2
		}
	}
	return nil
}

func (d *Daemon) setFailure(err error) {
	d.mu.Lock()
	if d.failure == nil {
		d.failure = err
		if d.connectionCancel != nil {
			d.connectionCancel()
		}
	}
	for _, active := range d.active {
		active.cancel()
	}
	d.mu.Unlock()
}
func (d *Daemon) cancelAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, active := range d.active {
		active.cancel()
	}
}

func (d *Daemon) capabilities() p.Capabilities {
	d.mu.Lock()
	failure := d.failure
	d.mu.Unlock()
	c := p.Capabilities{Readiness: d.readiness, Executor: d.config.Executor, Ready: failure == nil, Slots: d.config.Slots, Fingerprint: fmt.Sprintf("camp-remote-v%d:%s:%s:%s", p.Version, runtime.GOOS, runtime.GOARCH, d.config.Executor)}
	if d.config.Executor == "native" {
		c.DSHVersion = "0.2.1-rc.3"
		c.LayerFSVersion = "1.1.0"
		c.Fingerprint += ":" + c.DSHVersion + ":" + c.LayerFSVersion
		if d.native != nil {
			c.Fingerprint += ":" + d.native.Fingerprint()
		}
	}
	if failure != nil {
		c.Reason = "Execution unavailable: " + failure.Error()
	}
	return c
}
func (d *Daemon) sources() []p.Source {
	result := []p.Source{}
	private, privateErr := filepath.EvalSymlinks(d.config.DataDir)
	for alias, directory := range d.config.Sources {
		item := p.Source{ID: alias, Alias: alias, Revision: sourceRevision(directory), Available: true}
		info, err := os.Lstat(directory)
		resolved, resolveErr := filepath.EvalSymlinks(directory)
		relative, relativeErr := filepath.Rel(private, directory)
		containsData, containsErr := filepath.Rel(directory, private)
		if !filepath.IsAbs(directory) || err != nil || resolveErr != nil || privateErr != nil || relativeErr != nil || containsErr != nil || resolved != directory || !info.IsDir() || relative == "." || filepath.IsLocal(relative) || containsData == "." || filepath.IsLocal(containsData) {
			item.Available = false
			item.Reason = "Source must be an existing directory outside Runner data"
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Alias < result[j].Alias })
	return result
}
func (d *Daemon) activeIDs(ctx context.Context) ([]string, error) {
	ops, err := d.journal.operations(ctx)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, op := range ops {
		ids = append(ids, op.Command.OperationID)
	}
	return ids, nil
}

func (d *Daemon) connect(ctx context.Context) error {
	address, err := endpoint(d.config.ServerURL, "/api/daemon/connect")
	if err != nil {
		return err
	}
	address = strings.Replace(address, "https:", "wss:", 1)
	address = strings.Replace(address, "http:", "ws:", 1)
	connection, response, err := websocket.Dial(ctx, address, &websocket.DialOptions{HTTPClient: d.client, HTTPHeader: http.Header{"Authorization": []string{"Bearer " + d.config.Credential}}})
	if err != nil {
		if response != nil && (response.StatusCode == 401 || response.StatusCode == 403) {
			return &httpError{Status: response.StatusCode}
		}
		return err
	}
	defer connection.CloseNow()
	defer d.recordConnection(false)
	connection.SetReadLimit(p.MaxFrameBytes)
	sessionCtx, stop := context.WithCancel(ctx)
	defer stop()
	d.mu.Lock()
	d.connectionCancel = stop
	d.mu.Unlock()
	active, err := d.activeIDs(ctx)
	if err != nil {
		return err
	}
	hello := p.Message{Type: "hello", Hello: &p.Hello{Version: p.Version, StorageID: d.config.StorageID, BootID: d.bootID, Capabilities: d.capabilities(), Sources: d.sources(), Active: active}}
	hello.RequestID = d.newGrantRequest()
	if err = wsjson.Write(sessionCtx, connection, hello); err != nil {
		return err
	}
	type controlFrame struct {
		message p.Message
		err     error
	}
	incoming := make(chan controlFrame, 16)
	go func() {
		for {
			var message p.Message
			err := wsjson.Read(sessionCtx, connection, &message)
			select {
			case incoming <- controlFrame{message: message, err: err}:
			case <-sessionCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	outbox := time.NewTicker(250 * time.Millisecond)
	defer outbox.Stop()
	var epoch int64
	send := func(message p.Message) error {
		message.Epoch = epoch
		writeCtx, cancel := context.WithTimeout(sessionCtx, 10*time.Second)
		defer cancel()
		return wsjson.Write(writeCtx, connection, message)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame := <-incoming:
			if frame.err != nil {
				return frame.err
			}
			message := frame.message
			switch message.Type {
			case "welcome":
				if message.Version != p.Version {
					return &connectionRefusedError{reason: fmt.Sprintf("Upgrade required: Runner protocol %d does not match Server protocol %d. Install a matching Camp Runner package.", p.Version, message.Version)}
				}
				epoch = message.Epoch
				d.mu.Lock()
				d.connectionReason = ""
				d.mu.Unlock()
				if err := d.recordConnection(true); err != nil {
					return err
				}
			case "command":
				if message.Command == nil {
					return errors.New("Camp sent an empty command")
				}
				if err = validateCommand(*message.Command); err != nil {
					return err
				}
				_, err = d.journal.accept(ctx, *message.Command)
				if err != nil {
					d.setFailure(err)
					return err
				}
				if err = send(p.Message{Type: "accepted", RequestID: d.newGrantRequest(), CommandID: message.Command.ID, OperationID: message.Command.OperationID}); err != nil {
					return err
				}
			case "grant":
				d.setGrant(message.OperationID, message.RequestID, message.LeaseMS, message.LeaseUntil)
			case "cancel":
				if err = d.journal.cancel(ctx, message.OperationID); err != nil {
					d.setFailure(err)
					return err
				}
				d.mu.Lock()
				if active := d.active[message.OperationID]; active != nil {
					active.cancelled = true
					active.cancel()
				}
				d.mu.Unlock()
			case "event_ack":
				if err = d.journal.acknowledge(ctx, message.OperationID, message.Sequence); err != nil {
					d.setFailure(err)
					return err
				}
			case "rejected":
				if message.OperationID == "" {
					reason := message.Reason
					if reason == "" {
						reason = "Camp refused this Runner connection. Check the Server and Runner versions before restarting."
					}
					return &connectionRefusedError{reason: reason}
				}
				if err = d.journal.receipt(ctx, message.OperationID, p.Receipt{Disposition: "rejected", Reason: message.Reason}); err != nil {
					d.setFailure(err)
					return err
				}
				d.mu.Lock()
				if active := d.active[message.OperationID]; active != nil {
					active.cancel()
				}
				delete(d.grants, message.OperationID)
				d.mu.Unlock()
				d.pruneAcknowledged(ctx, message.OperationID)
			}
		case <-heartbeat.C:
			if err := d.recordConnection(true); err != nil {
				return err
			}
			active, err := d.activeIDs(ctx)
			if err != nil {
				return err
			}
			if err = send(p.Message{Type: "heartbeat", RequestID: d.newGrantRequest(), Active: active}); err != nil {
				return err
			}
		case <-outbox.C:
			events, err := d.journal.events(ctx)
			if err != nil {
				d.setFailure(err)
				return err
			}
			for i := range events {
				if err = send(p.Message{Type: "event", Event: &events[i]}); err != nil {
					return err
				}
			}
		}
	}
}

func validateCommand(command p.Command) error {
	if command.ID == "" || len(command.ID) > 128 || command.OperationID == "" || len(command.OperationID) > 128 || !filepath.IsLocal(command.OperationID) || strings.ContainsAny(command.OperationID, "/\\") {
		return errors.New("Invalid command identity")
	}
	if _, err := time.Parse(time.RFC3339Nano, command.Deadline); err != nil {
		return errors.New("Invalid execution deadline")
	}
	switch command.Kind {
	case "run.start":
		if command.Spec == nil || command.Spec.AttemptID != command.OperationID || command.Spec.TimeoutSeconds < 1 || command.Spec.TimeoutSeconds > 900 {
			return errors.New("Invalid execution specification")
		}
	case "source.capture":
		if command.Source == nil || command.Source.SourceID == "" {
			return errors.New("Invalid source capture")
		}
	default:
		return errors.New("Unsupported command")
	}
	return nil
}

func (d *Daemon) newGrantRequest() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.grantRequests == nil {
		d.grantRequests = map[string]time.Time{}
	}
	now := time.Now()
	for id, sent := range d.grantRequests {
		if now.Sub(sent) > 2*time.Minute {
			delete(d.grantRequests, id)
		}
	}
	if len(d.grantRequests) >= 256 {
		var oldestID string
		oldest := now
		for id, sent := range d.grantRequests {
			if sent.Before(oldest) {
				oldestID, oldest = id, sent
			}
		}
		delete(d.grantRequests, oldestID)
	}
	id := randomID()
	d.grantRequests[id] = now
	return id
}

func (d *Daemon) setGrant(id, requestID string, milliseconds int64, until string) {
	if milliseconds <= 0 {
		return
	}
	if milliseconds > 60000 {
		milliseconds = 60000
	}
	duration := time.Duration(milliseconds) * time.Millisecond
	d.mu.Lock()
	defer d.mu.Unlock()
	requested, known := d.grantRequests[requestID]
	if !known || time.Since(requested) > 2*time.Minute {
		return
	}
	// Absolute expiry can only shorten a grant. The running watchdog always uses
	// a local monotonic deadline, so clock corrections cannot extend execution.
	if timestamp, err := time.Parse(time.RFC3339Nano, until); err == nil && time.Until(timestamp) < duration {
		duration = time.Until(timestamp)
	}
	if duration <= 0 {
		return
	}
	// Anchor to this exact request even when an old grant arrives after newer
	// heartbeats. Delivery delay and server clock skew cannot extend authority.
	deadline := requested.Add(duration - min(duration/10, 500*time.Millisecond))
	if !time.Now().Before(deadline) {
		return
	}
	d.grants[id] = grant{until: deadline}
	if active := d.active[id]; active != nil {
		active.deadline = deadline
	}
}

func (d *Daemon) schedule(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	uploading := map[string]bool{}
	uploadsDone := make(chan string, 16)
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-uploadsDone:
			delete(uploading, id)
		case <-ticker.C:
		}
		ops, err := d.journal.operations(ctx)
		if err != nil {
			if ctx.Err() == nil {
				d.setFailure(err)
			}
			return
		}
		conversationOwner := map[string]string{}
		for _, op := range ops {
			if spec := op.Command.Spec; spec != nil && spec.ConversationID != "" && conversationOwner[spec.ConversationID] == "" {
				conversationOwner[spec.ConversationID] = op.Command.OperationID
			}
		}
		d.mu.Lock()
		for _, active := range d.active {
			if !time.Now().Before(active.deadline) {
				active.cancel()
			}
		}
		d.mu.Unlock()
		for _, op := range ops {
			id := op.Command.OperationID
			if op.State == "uploading" && !uploading[id] {
				uploading[id] = true
				d.workers.Add(1)
				go func(op operation) {
					defer d.workers.Done()
					d.upload(ctx, op)
					select {
					case uploadsDone <- op.Command.OperationID:
					case <-ctx.Done():
					}
				}(op)
			} else if op.State == "accepted" {
				deadline, deadlineErr := time.Parse(time.RFC3339Nano, op.Command.Deadline)
				if deadlineErr != nil || !time.Now().Before(deadline) {
					result := p.Result{CompletionID: randomID(), Status: "interrupted", Error: "The execution deadline elapsed before this operation started.", Checks: []p.CheckResult{}, CleanupConfirmed: true}
					if err := d.journal.finish(ctx, id, result, ""); err != nil {
						d.setFailure(err)
					}
					continue
				}
				if op.Cancelled {
					result := p.Result{CompletionID: randomID(), Status: "cancelled", Error: "Cancelled before execution started.", Checks: []p.CheckResult{}, CleanupConfirmed: true}
					if err := d.journal.finish(ctx, id, result, ""); err != nil {
						d.setFailure(err)
					}
					continue
				}
				d.mu.Lock()
				g, authorized := d.grants[id]
				ready := d.failure == nil && len(d.active) < d.config.Slots && authorized && time.Now().Before(g.until)
				// Server may commit and dispatch the next request before its prior
				// HTTP response reaches us. Native session eligibility becomes usable
				// only with that receipt; serialize every generation of a conversation
				// through the terminal journal transaction, not just process exit.
				if spec := op.Command.Spec; spec != nil && spec.ConversationID != "" && conversationOwner[spec.ConversationID] != id {
					ready = false
				}
				if !ready {
					d.mu.Unlock()
					continue
				}
				// Register the active cancellation handle atomically with beginning
				// execution. A persisted cancel/rejection cannot miss this handoff.
				started, err := d.journal.begin(ctx, id)
				if err != nil {
					d.mu.Unlock()
					d.setFailure(err)
					continue
				}
				if !started {
					d.mu.Unlock()
					continue
				}
				runCtx, cancel := context.WithCancel(ctx)
				d.active[id] = &activeOperation{cancel: cancel, deadline: g.until}
				d.mu.Unlock()
				d.workers.Add(1)
				go func(op operation) { defer d.workers.Done(); defer cancel(); d.execute(runCtx, op) }(op)
			}
		}
	}
}
