package remoteruns

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (m *Manager) scheduleSources(ctx context.Context) error {
	imports, err := m.store.ListActiveSourceImports(ctx)
	if err != nil {
		return err
	}
	for _, item := range imports {
		runner, err := m.store.GetRunner(ctx, item.RunnerID)
		if err != nil {
			return err
		}
		if (item.ExpiresAt != "" && expired(item.ExpiresAt)) || !runner.Enabled || runner.Revoked {
			if _, err = m.store.FailSourceImport(ctx, item.ID, item.RunnerID, "", "", "interrupted", "Source capture authorization ended before a confirmed result."); err != nil {
				return err
			}
			continue
		}
		if item.Status == "cancelling" {
			continue
		}
		if !m.IsOnline(item.RunnerID) {
			continue
		}
		var capabilities p.Capabilities
		_ = json.Unmarshal(runner.Capabilities, &capabilities)
		if !capabilities.Ready {
			continue
		}
		item, err = m.store.ClaimSourceImport(ctx, item.ID, item.RunnerID, 5*time.Minute)
		if err != nil {
			if !transient(err) {
				continue
			}
			return err
		}
		command := p.Command{ID: item.CommandID, Kind: "source.capture", OperationID: item.ID, Deadline: item.ExpiresAt, Source: &p.SourceCapture{SourceID: item.SourceID, Revision: item.SourceRevision}}
		data, _ := json.Marshal(command)
		if err = m.store.SaveSourceImportCommand(ctx, item.ID, data); err != nil {
			if !transient(err) {
				continue
			}
			return err
		}
	}
	return nil
}
func (m *Manager) grantSource(ctx context.Context, peer *peer, id, requestID string) error {
	item, err := m.store.GetSourceImport(ctx, id)
	if err != nil {
		return err
	}
	if item.RunnerID != peer.runnerID {
		return problem("runner_mismatch", "Source capture belongs to another runner.")
	}
	if item.CompletionID != "" {
		return nil
	}
	if terminal(item.Status) || expired(item.ExpiresAt) {
		return problem("import_expired", "Source capture is no longer authorized.")
	}
	if item.Status == "cancelling" {
		return peer.send(p.Message{Type: "cancel", OperationID: id})
	}
	end, _ := time.Parse(time.RFC3339Nano, item.ExpiresAt)
	if until := time.Now().Add(m.config.LeaseDuration); end.After(until) {
		end = until
	}
	return m.grant(peer, id, end.UTC().Format(time.RFC3339Nano), requestID)
}
func sourceReceipt(item storage.SourceImport) *p.Receipt {
	if !terminal(item.Status) {
		return nil
	}
	disposition := "committed"
	if item.CompletionID == "" {
		disposition = "rejected"
	}
	return &p.Receipt{Disposition: disposition, Status: item.Status, Reason: item.Error}
}
func (m *Manager) sourceForRequest(w http.ResponseWriter, r *http.Request, runner storage.Runner) (storage.SourceImport, bool) {
	item, err := m.store.GetSourceImport(r.Context(), r.PathValue("id"))
	if err != nil {
		m.disposition(w, err)
		return item, false
	}
	if item.RunnerID != runner.ID {
		machineError(w, 403, "Source capture belongs to another runner.")
		return item, false
	}
	if receipt := sourceReceipt(item); receipt != nil {
		sendJSON(w, 200, receipt)
		return item, false
	}
	if expired(item.ExpiresAt) {
		m.disposition(w, problem("import_expired", "Source capture expired."))
		return item, false
	}
	return item, true
}
func (m *Manager) sourceFinalizing(w http.ResponseWriter, r *http.Request, runner storage.Runner) {
	item, ok := m.sourceForRequest(w, r, runner)
	if ok {
		sendJSON(w, 200, leaseReceipt(item.ExpiresAt))
	}
}
func (m *Manager) sourceUpload(w http.ResponseWriter, r *http.Request, runner storage.Runner) {
	item, ok := m.sourceForRequest(w, r, runner)
	if !ok {
		return
	}
	if item.Status == "cancelling" {
		sendJSON(w, 200, p.Receipt{Disposition: "accepted"})
		return
	}
	hash, path, err := m.receiveArtifact(w, r, item.ID)
	if err == nil {
		err = m.store.SaveSourceImportArtifact(r.Context(), item.ID, runner.ID, hash, path)
	}
	if err != nil {
		if current, e := m.store.GetSourceImport(r.Context(), item.ID); e == nil && current.Status == "cancelling" && !expired(current.ExpiresAt) {
			sendJSON(w, 200, p.Receipt{Disposition: "accepted"})
			return
		}
		if transient(err) {
			m.disposition(w, err)
		} else {
			m.rejectSourceResult(w, r, item, err)
		}
		return
	}
	sendJSON(w, 200, p.Receipt{Disposition: "accepted"})
}
func (m *Manager) completeSource(w http.ResponseWriter, r *http.Request, runner storage.Runner, result p.Result, hash string) {
	item, err := m.store.GetSourceImport(r.Context(), r.PathValue("id"))
	if err != nil {
		m.disposition(w, err)
		return
	}
	if item.RunnerID != runner.ID {
		machineError(w, 403, "Source capture belongs to another runner.")
		return
	}
	if item.CompletionID != "" && (item.CompletionID != result.CompletionID || item.ResultDigest != hash) {
		m.disposition(w, problem("request_conflict", "This source operation already has a different completion."))
		return
	}
	if receipt := sourceReceipt(item); receipt != nil {
		sendJSON(w, 200, receipt)
		return
	}
	if expired(item.ExpiresAt) {
		m.disposition(w, problem("import_expired", "Source capture expired."))
		return
	}
	if !validResultText(result.CompletionID, 128, false) || !terminal(result.Status) || !validResultText(result.Error, storage.MaxRunErrorSize, true) {
		m.rejectSourceResult(w, r, item, problem("invalid_result", "Invalid source result."))
		return
	}
	if !result.CleanupConfirmed {
		result.Status = "interrupted"
		result.Error = "Source capture cleanup could not be confirmed."
	}
	if item.Status == "cancelling" && result.CleanupConfirmed {
		result.Status = "cancelled"
		result.Error = "Source import cancelled. Original files were not modified."
	}
	if result.Status == "succeeded" {
		if result.Archive == nil || result.Archive.SHA256 != item.ArtifactDigest || item.ArtifactPath == "" {
			m.rejectSourceResult(w, r, item, problem("invalid_artifact", "Source manifest does not match the accepted archive."))
			return
		}
		manifest, e := artifactManifest(item.ArtifactPath)
		if e != nil {
			m.disposition(w, e)
			return
		}
		if manifest.SHA256 != result.Archive.SHA256 || manifest.Bytes != result.Archive.Bytes {
			m.rejectSourceResult(w, r, item, problem("invalid_artifact", "Source archive integrity check failed."))
			return
		}
		f, e := os.Open(item.ArtifactPath)
		if e != nil {
			m.disposition(w, e)
			return
		}
		imported, e := m.files.ImportArchive(r.Context(), item.ResourceID, f)
		f.Close()
		if e != nil {
			result.Status = "failed"
			result.Error = "Source archive was rejected: " + m.redact(e.Error())
		} else {
			completed, finishErr := m.store.FinishSourceImport(r.Context(), item.ID, runner.ID, result.CompletionID, hash, storage.Resource{ID: item.ResourceID, CurrentCommit: imported.Commit})
			err = finishErr
			if err == nil {
				item = completed
			} else if current, e := m.store.GetSourceImport(r.Context(), item.ID); e == nil && current.Status == "cancelling" && result.CleanupConfirmed && !expired(current.ExpiresAt) {
				item, err = m.store.FailSourceImport(r.Context(), item.ID, runner.ID, result.CompletionID, hash, "cancelled", "Source import cancelled after capture. Original files were not modified.")
			}
			if err != nil && !transient(err) {
				// A Room name or authority can change after capture starts. Record
				// the rejected publication as a terminal outcome before acknowledging
				// it, keeping the original completion and archive identities intact.
				item, err = m.store.CompleteSourceImportFailure(r.Context(), item.ID, runner.ID, result.CompletionID, hash, "failed", "Source import could not be published: "+m.redact(err.Error()), result.CleanupConfirmed)
			}
			if err != nil {
				m.disposition(w, err)
				return
			}
		}
	}
	if result.Status != "succeeded" {
		item, err = m.store.CompleteSourceImportFailure(r.Context(), item.ID, runner.ID, result.CompletionID, hash, result.Status, m.redact(result.Error), result.CleanupConfirmed)
		if err != nil {
			m.disposition(w, err)
			return
		}
	}
	m.Wake()
	if !result.CleanupConfirmed {
		m.StopRunner(runner.ID)
	}
	sendJSON(w, 200, sourceReceipt(item))
}

func (m *Manager) rejectSourceResult(w http.ResponseWriter, r *http.Request, item storage.SourceImport, reason error) {
	item, err := m.store.FailSourceImport(r.Context(), item.ID, item.RunnerID, "", "", "interrupted", "Runner source result rejected: "+m.redact(reason.Error()))
	if err != nil {
		m.disposition(w, err)
		return
	}
	m.Wake()
	sendJSON(w, 200, sourceReceipt(item))
}
