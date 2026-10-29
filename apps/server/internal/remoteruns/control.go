package remoteruns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type peer struct {
	runnerID   string
	epoch      int64
	connection *websocket.Conn
	context    context.Context
	cancel     context.CancelFunc
	writeMu    sync.Mutex
	lastSeen   time.Time // protected by Manager.mu
}

func (peer *peer) close() { peer.cancel(); _ = peer.connection.CloseNow() }
func (peer *peer) send(message p.Message) error {
	peer.writeMu.Lock()
	defer peer.writeMu.Unlock()
	message.Epoch = peer.epoch
	ctx, cancel := context.WithTimeout(peer.context, 5*time.Second)
	defer cancel()
	return wsjson.Write(ctx, peer.connection, message)
}

func (m *Manager) connect(w http.ResponseWriter, r *http.Request, runner storage.Runner) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(p.MaxFrameBytes)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stopLifetime := context.AfterFunc(m.ctx, cancel)
	defer stopLifetime()
	handshake, stop := context.WithTimeout(ctx, 10*time.Second)
	var message p.Message
	err = wsjson.Read(handshake, connection, &message)
	stop()
	if err != nil || message.Type != "hello" || message.Hello == nil {
		return
	}
	hello := message.Hello
	if hello.Version != p.Version {
		reason := fmt.Sprintf("Upgrade required: Runner protocol %d does not match Server protocol %d. Install a matching Camp Runner package.", hello.Version, p.Version)
		if !m.IsOnline(runner.ID) {
			if err := m.store.RecordRunnerConnectionIssue(ctx, runner.ID, reason); err != nil {
				m.logger.Error("Could not record Runner compatibility", "code", errorCode(err))
			}
		}
		reply, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		_ = wsjson.Write(reply, connection, p.Message{Type: "rejected", Version: p.Version, Reason: reason})
		return
	}
	if hello.StorageID != runner.StorageID || hello.BootID == "" || hello.Capabilities.Slots < 1 || hello.Capabilities.Slots > p.MaxSlots || (hello.Capabilities.Executor != "native" && hello.Capabilities.Executor != "fixture") || (hello.Capabilities.Executor == "fixture" && !m.config.AllowFixture) {
		return
	}
	caps, _ := json.Marshal(hello.Capabilities)
	sources := make([]storage.RunnerSource, 0, len(hello.Sources))
	for _, source := range hello.Sources {
		sources = append(sources, storage.RunnerSource{ID: source.ID, Alias: source.Alias, Revision: source.Revision, Available: source.Available, Reason: source.Reason})
	}
	connected, err := m.store.ConnectRunner(ctx, runner.ID, storage.RunnerConnection{StorageID: hello.StorageID, Runtime: hello.Capabilities.Executor, Slots: hello.Capabilities.Slots, Capabilities: caps, Sources: sources})
	if err != nil {
		return
	}
	peer := &peer{runnerID: runner.ID, epoch: connected.ConnectionGeneration, connection: connection, context: ctx, cancel: cancel, lastSeen: time.Now()}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	old := m.peers[runner.ID]
	m.peers[runner.ID] = peer
	m.mu.Unlock()
	if old != nil {
		old.close()
	}
	defer func() {
		m.mu.Lock()
		if m.peers[runner.ID] == peer {
			delete(m.peers, runner.ID)
		}
		m.mu.Unlock()
	}()
	if peer.send(p.Message{Type: "welcome", Version: p.Version, RunnerID: runner.ID}) != nil {
		return
	}
	if err = m.reconcile(ctx, peer, hello.Active, message.RequestID); err != nil {
		return
	}
	m.Wake()
	for {
		read, stop := context.WithTimeout(ctx, 35*time.Second)
		message = p.Message{}
		err = wsjson.Read(read, connection, &message)
		stop()
		if err != nil {
			return
		}
		m.mu.Lock()
		current := m.peers[runner.ID] == peer
		if current {
			peer.lastSeen = time.Now()
		}
		m.mu.Unlock()
		if !current || message.Epoch != peer.epoch {
			return
		}
		if err = m.store.TouchRunner(ctx, runner.ID, peer.epoch); err != nil {
			return
		}
		switch message.Type {
		case "heartbeat":
			err = m.reconcile(ctx, peer, message.Active, message.RequestID)
		case "accepted":
			var attempt storage.Attempt
			attempt, err = m.store.GetAttempt(ctx, message.OperationID)
			if err == nil {
				if attempt.RunnerID != runner.ID || attempt.CommandID != message.CommandID {
					err = errors.New("Command acknowledgement identity mismatch")
				} else {
					if receipt := storedReceipt(attempt); receipt != nil && receipt.Disposition == "committed" {
						// A retransmitted acceptance can race the final HTTP response.
						// Only that idempotent response settles the daemon's result.
						break
					}
					attempt, err = m.store.AcceptAttempt(ctx, attempt.ID, runner.ID)
					if err == nil {
						if attempt.CancelRequested {
							err = peer.send(p.Message{Type: "cancel", OperationID: attempt.ID})
						} else {
							err = m.grant(peer, attempt.ID, attempt.LeaseUntil, message.RequestID)
						}
					}
				}
			} else if errorCode(err) == "not_found" {
				var source storage.SourceImport
				source, err = m.store.GetSourceImport(ctx, message.OperationID)
				if err == nil && (source.RunnerID != runner.ID || source.CommandID != message.CommandID) {
					err = problem("request_conflict", "Source acceptance does not match the dispatched command.")
				}
				if err == nil {
					err = m.store.AcknowledgeRunnerCommand(ctx, runner.ID, message.CommandID)
				}
				if err == nil {
					err = m.grantSource(ctx, peer, message.OperationID, message.RequestID)
				}
			}
		case "event":
			if message.Event == nil {
				return
			}
			event := *message.Event
			data, _ := json.Marshal(event)
			event.Text = m.redact(event.Text)
			_, err = m.store.RecordAttemptEvent(ctx, runner.ID, event.OperationID, event.Sequence, event.Kind, event.Text, digest(data))
			if errorCode(err) == "not_found" {
				_, err = m.store.RecordSourceImportEvent(ctx, runner.ID, event.OperationID, event.Sequence, event.Kind, event.Text, digest(data))
			}
			if err == nil {
				err = peer.send(p.Message{Type: "event_ack", OperationID: event.OperationID, Sequence: event.Sequence})
			}
		default:
			return
		}
		if err != nil {
			if transient(err) {
				return
			}
			id := message.OperationID
			if message.Event != nil {
				id = message.Event.OperationID
			}
			if peer.send(p.Message{Type: "rejected", OperationID: id, Reason: m.redact(err.Error())}) != nil {
				return
			}
		}
	}
}

func (m *Manager) reconcile(ctx context.Context, peer *peer, active []string, requestID string) error {
	if requestID == "" || len(requestID) > 128 {
		return problem("invalid_message", "Lease reconciliation needs a bounded request identity.")
	}
	if len(active) > 256 {
		return problem("invalid_message", "Too many active operations.")
	}
	for _, id := range active {
		a, err := m.store.GetAttempt(ctx, id)
		if err == nil {
			if a.RunnerID != peer.runnerID {
				return problem("runner_mismatch", "Operation belongs to another runner.")
			}
			if a.Phase == "finished" {
				if receipt := storedReceipt(a); receipt != nil && receipt.Disposition == "committed" {
					continue
				}
				if err = peer.send(p.Message{Type: "rejected", OperationID: id, Reason: "Operation is already terminal; query its completion receipt."}); err != nil {
					return err
				}
				continue
			}
			if a.Phase == "dispatching" {
				a, err = m.store.AcceptAttempt(ctx, id, peer.runnerID)
			}
			if err == nil {
				a, err = m.store.RenewAttempt(ctx, id, peer.runnerID, m.config.LeaseDuration)
			}
			if err == nil {
				if a.CancelRequested {
					err = peer.send(p.Message{Type: "cancel", OperationID: id})
				} else {
					err = m.grant(peer, id, a.LeaseUntil, requestID)
				}
			}
		} else if errorCode(err) == "not_found" {
			err = m.grantSource(ctx, peer, id, requestID)
		}
		if err != nil {
			if transient(err) {
				return err
			}
			if err = peer.send(p.Message{Type: "rejected", OperationID: id, Reason: m.redact(err.Error())}); err != nil {
				return err
			}
		}
	}
	return nil
}
func (m *Manager) grant(peer *peer, id, until, requestID string) error {
	end, err := time.Parse(time.RFC3339Nano, until)
	if err != nil {
		return err
	}
	return peer.send(p.Message{Type: "grant", RequestID: requestID, OperationID: id, LeaseUntil: until, LeaseMS: max(0, time.Until(end).Milliseconds())})
}
func (m *Manager) dispatch(ctx context.Context, peer *peer) error {
	commands, err := m.store.ListRunnerCommands(ctx, peer.runnerID)
	if err != nil {
		return err
	}
	for _, row := range commands {
		var command p.Command
		if err = json.Unmarshal(row.Payload, &command); err != nil {
			return err
		}
		if row.Kind == "run.cancel" || row.Kind == "source.cancel" {
			if err = peer.send(p.Message{Type: "cancel", OperationID: row.OperationID, CommandID: row.ID}); err != nil {
				return err
			}
			continue
		}
		if command.Kind == "run.start" {
			a, err := m.store.ValidateAttempt(ctx, row.OperationID, peer.runnerID, true)
			if err != nil {
				continue
			}
			if err = peer.send(p.Message{Type: "command", Command: &command}); err != nil {
				return err
			}
			if a.CancelRequested {
				if err = peer.send(p.Message{Type: "cancel", OperationID: a.ID}); err != nil {
					return err
				}
			}
		} else if command.Kind == "source.capture" {
			if err = peer.send(p.Message{Type: "command", Command: &command}); err != nil {
				return err
			}
		}
	}
	return nil
}
func transient(err error) bool { return err != nil && errorCode(err) == "internal_error" }
