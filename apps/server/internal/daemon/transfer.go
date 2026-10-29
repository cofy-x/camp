package daemon

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func (d *Daemon) download(ctx context.Context, artifact p.Artifact, destination string) error {
	if artifact.Bytes < 0 || artifact.Bytes > p.MaxArchiveBytes || len(artifact.SHA256) != 64 {
		return errors.New("Invalid input archive manifest")
	}
	target, err := endpoint(d.config.ServerURL, artifact.URL)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+d.config.Credential)
	response, err := d.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return &httpError{Status: response.StatusCode}
	}
	archive, err := os.CreateTemp(filepath.Dir(destination), ".input-")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(response.Body, p.MaxArchiveBytes+1))
	if err != nil {
		return err
	}
	if size != artifact.Bytes || hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return errors.New("Input archive integrity check failed")
	}
	if _, err = archive.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return resources.ExtractArchive(ctx, archive, destination)
}

func (d *Daemon) upload(ctx context.Context, op operation) {
	// Each retry is bounded; a slow/disconnected data endpoint does not hold the
	// control reader or lease watchdog. No retry runs the executor again.
	defer func() {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
	}()
	if op.Result == nil {
		d.setFailure(errors.New("Uploading operation has no durable result"))
		return
	}
	result := *op.Result
	id := op.Command.OperationID
	base := "/api/daemon/operations/" + id
	transferCtx, stop := context.WithTimeout(ctx, 60*time.Second)
	defer stop()
	var receipt p.Receipt
	// Revoke execution authority for every completed executor, including readers
	// without an archive. Query durable completion before touching local bytes:
	// a lost response is recoverable even if scratch was subsequently damaged.
	if err := requestJSON(transferCtx, d.client, d.config, "POST", base+"/finalizing", struct{}{}, &receipt); err != nil {
		if terminalHTTP(err) {
			d.finishReceipt(ctx, op, p.Receipt{Disposition: "rejected", Reason: err.Error()})
		}
		return
	}
	if receipt.Disposition == "committed" || receipt.Disposition == "rejected" {
		d.finishReceipt(ctx, op, receipt)
		return
	}
	if result.Archive != nil {
		if err := verifyArchive(op.ArchivePath, *result.Archive, false); err != nil {
			d.setFailure(errors.New("A retained result archive failed integrity verification"))
			return
		}
		if err := d.putArchive(transferCtx, base+"/artifact", op.ArchivePath, *result.Archive, &receipt); err != nil {
			if terminalHTTP(err) {
				d.finishReceipt(ctx, op, p.Receipt{Disposition: "rejected", Reason: err.Error()})
			}
			return
		}
		if receipt.Disposition == "rejected" {
			d.finishReceipt(ctx, op, receipt)
			return
		}
	}
	pending, err := d.journal.pendingEvents(transferCtx, id)
	if err != nil {
		d.setFailure(err)
		return
	}
	if pending {
		return
	}
	if err = requestJSON(transferCtx, d.client, d.config, "POST", base+"/complete", result, &receipt); err != nil {
		if terminalHTTP(err) {
			d.finishReceipt(ctx, op, p.Receipt{Disposition: "rejected", Reason: err.Error()})
		}
		return
	}
	if receipt.Disposition == "committed" || receipt.Disposition == "rejected" {
		d.finishReceipt(ctx, op, receipt)
	}
}

func terminalHTTP(err error) bool {
	var value *httpError
	return errors.As(err, &value) && (value.Status == 401 || value.Status == 403 || value.Status == 404 || value.Status == 410)
}

func (d *Daemon) putArchive(ctx context.Context, path, name string, artifact p.Artifact, receipt *p.Receipt) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	target, err := endpoint(d.config.ServerURL, path)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, "PUT", target, file)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+d.config.Credential)
	request.Header.Set("Content-Type", "application/x-tar")
	request.Header.Set("X-Camp-SHA256", artifact.SHA256)
	request.ContentLength = artifact.Bytes
	response, err := d.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &httpError{Status: response.StatusCode}
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(receipt)
}

func (d *Daemon) finishReceipt(ctx context.Context, op operation, receipt p.Receipt) {
	if err := d.journal.receipt(ctx, op.Command.OperationID, receipt); err != nil {
		d.setFailure(err)
		return
	}
	d.mu.Lock()
	delete(d.grants, op.Command.OperationID)
	d.mu.Unlock()
	if err := os.RemoveAll(filepath.Join(d.config.DataDir, "operations", op.Command.OperationID)); err != nil {
		fmt.Fprintln(d.output, "The final result was acknowledged, but local scratch cleanup failed.")
	}
}

func sessionKey(spec *p.ExecutionSpec) string {
	return spec.ConversationID + ":" + fmt.Sprint(spec.Generation)
}

// A crash after the receipt transaction may leave transfer files behind.
// Reconcile only known terminal operations after native startup has confirmed
// cleanup; unacknowledged artifacts and unrelated directories remain intact.
func (d *Daemon) pruneTerminalScratch(ctx context.Context) error {
	directory := filepath.Join(d.config.DataDir, "operations")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var state string
		err = d.journal.db.QueryRowContext(ctx, "SELECT state FROM commands WHERE operation_id=?", entry.Name()).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if state == "terminal" {
			if err = os.RemoveAll(filepath.Join(directory, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// A rejection can arrive while the executor still owns its files. The terminal
// receipt is durable immediately; scratch is removed only after execution stops.
func (d *Daemon) pruneAcknowledged(ctx context.Context, id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active[id] != nil {
		return
	}
	var state string
	if err := d.journal.db.QueryRowContext(ctx, "SELECT state FROM commands WHERE operation_id=?", id).Scan(&state); err != nil || state != "terminal" {
		return
	}
	if err := os.RemoveAll(filepath.Join(d.config.DataDir, "operations", id)); err != nil {
		fmt.Fprintln(d.output, "The final result was acknowledged, but local scratch cleanup failed.")
	}
}
