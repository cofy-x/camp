package remoteruns

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// Handler is a machine-authenticated boundary. Browser sessions are never
// accepted here; the server's outer Host and cross-origin policy still applies.
func (m *Manager) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/daemon/enroll", m.enroll)
	mux.HandleFunc("POST /api/daemon/model/{id}/v1/messages", m.modelProxy)
	machine := func(handler func(http.ResponseWriter, *http.Request, storage.Runner)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			runner, err := m.store.AuthenticateRunner(r.Context(), bearer(r))
			if err != nil {
				machineError(w, http.StatusUnauthorized, "Runner credentials are invalid.")
				return
			}
			if !runner.Enabled {
				terminal, err := m.terminalOperation(r, runner.ID)
				if err != nil {
					m.disposition(w, err)
					return
				}
				if !terminal {
					machineError(w, http.StatusForbidden, "This runner is disabled; only existing terminal receipts remain available.")
					return
				}
			}
			handler(w, r, runner)
		}
	}
	mux.HandleFunc("GET /api/daemon/connect", machine(m.connect))
	mux.HandleFunc("GET /api/daemon/runtime/{sha256}", machine(m.runtimeDownload))
	mux.HandleFunc("GET /api/daemon/operations/{id}/input", machine(m.input))
	mux.HandleFunc("POST /api/daemon/operations/{id}/finalizing", machine(m.finalizing))
	mux.HandleFunc("PUT /api/daemon/operations/{id}/artifact", machine(m.upload))
	mux.HandleFunc("POST /api/daemon/operations/{id}/complete", machine(m.complete))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" {
			machineError(w, http.StatusForbidden, "Browser credentials are not accepted by machine APIs.")
			return
		}
		host := r.Host
		if name, _, err := net.SplitHostPort(host); err == nil {
			host = name
		}
		ip := net.ParseIP(host)
		remote, _, remoteErr := net.SplitHostPort(r.RemoteAddr)
		remoteIP := net.ParseIP(remote)
		if r.TLS == nil && (remoteErr != nil || remoteIP == nil || !remoteIP.IsLoopback() || (host != "localhost" && (ip == nil || !ip.IsLoopback()))) {
			machineError(w, http.StatusForbidden, "Remote machine connections require TLS.")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (m *Manager) terminalOperation(r *http.Request, runnerID string) (bool, error) {
	id := r.PathValue("id")
	if id == "" {
		return false, nil
	}
	a, err := m.store.GetAttempt(r.Context(), id)
	if err == nil {
		return a.RunnerID == runnerID && a.Phase == "finished", nil
	}
	if errorCode(err) != "not_found" {
		return false, err
	}
	item, err := m.store.GetSourceImport(r.Context(), id)
	if errorCode(err) == "not_found" {
		return false, nil
	}
	return item.RunnerID == runnerID && terminal(item.Status), err
}
func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(v, "Bearer ")
}
func sendJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func machineError(w http.ResponseWriter, status int, message string) {
	sendJSON(w, status, map[string]any{"error": map[string]string{"code": "machine_request_failed", "message": message}})
}
func decodeMachine(w http.ResponseWriter, r *http.Request, value any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		machineError(w, 400, "Invalid machine request.")
		return false
	}
	var rest any
	if decoder.Decode(&rest) != io.EOF {
		machineError(w, 400, "Send one JSON object.")
		return false
	}
	return true
}
func (m *Manager) enroll(w http.ResponseWriter, r *http.Request) {
	var input p.Enrollment
	if !decodeMachine(w, r, &input, 16<<10) {
		return
	}
	runner, err := m.store.JoinRunner(r.Context(), storage.JoinRunnerInput{Token: input.Token, RegistrationID: input.RegistrationID, Credential: input.Credential, Name: input.Name, StorageID: input.StorageID})
	if err != nil {
		machineError(w, 403, "Enrollment is invalid, expired, revoked, or already used differently.")
		return
	}
	sendJSON(w, 200, p.Joined{RunnerID: runner.ID})
}
func (m *Manager) input(w http.ResponseWriter, r *http.Request, runner storage.Runner) {
	a, err := m.store.ValidateAttempt(r.Context(), r.PathValue("id"), runner.ID, false)
	if err != nil {
		machineError(w, 410, "Execution input is no longer authorized.")
		return
	}
	part := r.URL.Query().Get("part")
	if part != "project" && part != "references" {
		machineError(w, 400, "Unknown input part.")
		return
	}
	var command p.Command
	if json.Unmarshal(a.Command, &command) != nil || command.Spec == nil {
		machineError(w, 503, "Input is not prepared.")
		return
	}
	manifest := command.Spec.Project
	if part == "references" {
		if command.Spec.References == nil {
			machineError(w, 404, "No evidence archive.")
			return
		}
		manifest = *command.Spec.References
	}
	file, err := os.Open(filepath.Join(m.operationDir(a.ID), part+".tar"))
	if err != nil {
		machineError(w, 503, "Input archive is unavailable.")
		return
	}
	defer file.Close()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute))
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("X-Camp-SHA256", manifest.SHA256)
	http.ServeContent(w, r, part+".tar", time.Time{}, file)
}
func storedReceipt(a storage.Attempt) *p.Receipt {
	if a.Phase != "finished" {
		return nil
	}
	var receipt p.Receipt
	if json.Unmarshal(a.Receipt, &receipt) != nil || receipt.Disposition == "" {
		receipt = p.Receipt{Disposition: "rejected", Reason: "Execution is already terminal."}
	}
	return &receipt
}
func leaseReceipt(until string) p.Receipt {
	end, _ := time.Parse(time.RFC3339Nano, until)
	return p.Receipt{Disposition: "accepted", LeaseUntil: until, LeaseMS: max(0, time.Until(end).Milliseconds())}
}
func (m *Manager) disposition(w http.ResponseWriter, err error) {
	if transient(err) {
		machineError(w, 503, "The durable operation could not be recorded; retry the same request.")
	} else {
		sendJSON(w, 200, p.Receipt{Disposition: "rejected", Reason: m.redact(err.Error())})
	}
}
func (m *Manager) finalizing(w http.ResponseWriter, r *http.Request, runner storage.Runner) {
	var input struct{}
	if !decodeMachine(w, r, &input, 1024) {
		return
	}
	a, err := m.store.GetAttempt(r.Context(), r.PathValue("id"))
	if errorCode(err) == "not_found" {
		m.sourceFinalizing(w, r, runner)
		return
	}
	if err != nil {
		m.disposition(w, err)
		return
	}
	if a.RunnerID != runner.ID {
		machineError(w, 403, "Operation belongs to another runner.")
		return
	}
	if receipt := storedReceipt(a); receipt != nil {
		sendJSON(w, 200, receipt)
		return
	}
	a, err = m.store.BeginFinalizing(r.Context(), a.ID, runner.ID)
	if err != nil {
		m.disposition(w, err)
		return
	}
	m.stopProxy(a.ID)
	a, err = m.store.RenewAttempt(r.Context(), a.ID, runner.ID, m.config.LeaseDuration)
	if err != nil {
		m.disposition(w, err)
		return
	}
	sendJSON(w, 200, leaseReceipt(a.LeaseUntil))
}
func artifactManifest(path string) (p.Artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return p.Artifact{}, err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	return p.Artifact{SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: size}, err
}
func (m *Manager) receiveArtifact(w http.ResponseWriter, r *http.Request, id string) (string, string, error) {
	hash := r.Header.Get("X-Camp-SHA256")
	if raw, e := hex.DecodeString(hash); e != nil || len(raw) != 32 {
		return "", "", problem("invalid_artifact", "Archive digest is invalid.")
	}
	dir := m.operationDir(id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", err
	}
	path := filepath.Join(dir, "result-"+hash+".tar")
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Minute))
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, p.MaxArchiveBytes)
	defer r.Body.Close()
	manifest, err := writeArtifact(path, func(output io.Writer) error {
		h := sha256.New()
		_, err := io.Copy(io.MultiWriter(output, h), r.Body)
		if err != nil {
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				return problem("invalid_artifact", "Archive exceeds its transfer size limit.")
			}
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != hash {
			return problem("invalid_artifact", "Archive digest does not match its contents.")
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	if manifest.SHA256 != hash {
		return "", "", errors.New("Stored archive digest mismatch")
	}
	return hash, path, nil
}
func (m *Manager) upload(w http.ResponseWriter, r *http.Request, runner storage.Runner) {
	a, err := m.store.GetAttempt(r.Context(), r.PathValue("id"))
	if errorCode(err) == "not_found" {
		m.sourceUpload(w, r, runner)
		return
	}
	if err != nil {
		m.disposition(w, err)
		return
	}
	if a.RunnerID != runner.ID {
		machineError(w, 403, "Operation belongs to another runner.")
		return
	}
	if receipt := storedReceipt(a); receipt != nil {
		sendJSON(w, 200, receipt)
		return
	}
	if _, err = m.store.ValidateAttempt(r.Context(), a.ID, runner.ID, true); err != nil {
		if transient(err) {
			m.disposition(w, err)
		} else {
			m.rejectResult(w, r, a, err)
		}
		return
	}
	if a.Phase != "uploading" {
		m.rejectResult(w, r, a, problem("attempt_conflict", "Finish execution before uploading."))
		return
	}
	hash, path, err := m.receiveArtifact(w, r, a.ID)
	if err == nil {
		err = m.store.SaveAttemptArtifact(r.Context(), a.ID, runner.ID, hash, path)
	}
	if err != nil {
		if transient(err) {
			m.disposition(w, err)
		} else {
			m.rejectResult(w, r, a, err)
		}
		return
	}
	sendJSON(w, 200, p.Receipt{Disposition: "accepted"})
}
