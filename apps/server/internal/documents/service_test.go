package documents

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/documenttext"
	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type extractFunc func(context.Context, string, []byte) (documenttext.Result, error)

func (f extractFunc) Extract(ctx context.Context, name string, data []byte) (documenttext.Result, error) {
	return f(ctx, name, data)
}
func documentFixture(t *testing.T) (*storage.Store, *resources.Manager, storage.Resource, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	files := resources.New(filepath.Join(dir, "resources"))
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "brief.pdf"), []byte("immutable original bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "second.pdf"), []byte("immutable original bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	imported, err := files.Import(context.Background(), strings.Repeat("a", 32), source)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := store.CreateResource(context.Background(), storage.Resource{ID: strings.Repeat("a", 32), Name: "Documents", CurrentCommit: imported.Commit})
	if err != nil {
		t.Fatal(err)
	}
	return store, files, resource, filepath.Join(dir, "desktop.sqlite")
}
func waitDocument(t *testing.T, s *storage.Store, id string, status string) storage.Document {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		d, err := s.GetDocument(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if d.Status == status {
			return d
		}
		time.Sleep(5 * time.Millisecond)
	}
	d, _ := s.GetDocument(context.Background(), id)
	t.Fatalf("want %s, got %+v", status, d)
	return d
}
func TestPreparationSerializesAndShutdownRequeuesWithoutLosingOriginal(t *testing.T) {
	store, files, resource, _ := documentFixture(t)
	ctx := context.Background()
	started := make(chan string, 2)
	var active, maximum, calls atomic.Int32
	extractor := extractFunc(func(ctx context.Context, name string, data []byte) (documenttext.Result, error) {
		if string(data) != "immutable original bytes" {
			t.Error("original changed")
		}
		calls.Add(1)
		count := active.Add(1)
		defer active.Add(-1)
		maximum.CompareAndSwap(0, count)
		started <- name
		<-ctx.Done()
		return documenttext.Result{}, ctx.Err()
	})
	service, err := New(ctx, store, files, extractor, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Shutdown(ctx) })
	first, err := service.Prepare(ctx, resource.ID, resource.CurrentCommit, "brief.pdf", false)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	second, err := store.EnsureDocument(ctx, resource.ID, resource.CurrentCommit, "second.pdf", documenttext.Version, false)
	if err != nil {
		t.Fatal(err)
	}
	service.Wake()
	if err := service.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	waitDocument(t, store, first.ID, "queued")
	waitDocument(t, store, second.ID, "queued")
	if maximum.Load() != 1 || calls.Load() != 1 || active.Load() != 0 {
		t.Fatalf("concurrency or cancellation: max=%d calls=%d active=%d", maximum.Load(), calls.Load(), active.Load())
	}
	// A crash can leave extracting state; recovery is safe because no model was called.
	if _, err := store.ChangeDocument(ctx, first.ID, "extracting", nil); err != nil {
		t.Fatal(err)
	}
	successful := extractFunc(func(context.Context, string, []byte) (documenttext.Result, error) {
		calls.Add(1)
		return documenttext.Result{Format: "pdf", Sections: []documenttext.Section{{ID: "p1", Label: "Page 1", Text: "Prepared customer brief"}}}, nil
	})
	restarted, err := New(ctx, store, files, successful, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown(ctx)
	completed := waitDocument(t, store, first.ID, "ready")
	waitDocument(t, store, second.ID, "ready")
	if completed.Sections[0].Text != "Prepared customer brief" || calls.Load() != 3 {
		t.Fatal("recovery did not finish each queued job exactly once")
	}
	data, err := files.ReadFile(ctx, resource.ID, resource.CurrentCommit, "brief.pdf")
	if err != nil || string(data) != "immutable original bytes" {
		t.Fatal("preparation modified source", err)
	}
}
func TestPreparationFailureCategoriesAndExplicitRetry(t *testing.T) {
	for _, test := range []struct {
		code, status string
		retryable    bool
	}{{"unavailable", "unavailable", true}, {"no_text", "unreadable", false}, {"malformed", "unreadable", false}, {"timeout", "failed", true}, {"reader_failed", "failed", true}} {
		t.Run(test.code, func(t *testing.T) {
			store, files, resource, _ := documentFixture(t)
			ctx := context.Background()
			var calls atomic.Int32
			service, err := New(ctx, store, files, extractFunc(func(context.Context, string, []byte) (documenttext.Result, error) {
				calls.Add(1)
				return documenttext.Result{}, &documenttext.Failure{Code: test.code, Message: "Reader explanation", Retryable: test.retryable}
			}), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer service.Shutdown(ctx)
			d, err := service.Prepare(ctx, resource.ID, resource.CurrentCommit, "brief.pdf", false)
			if err != nil {
				t.Fatal(err)
			}
			failed := waitDocument(t, store, d.ID, test.status)
			if len(failed.Sections) != 0 || failed.Error.Code != test.code || failed.Error.Retryable != test.retryable {
				t.Fatal("failure misrepresented", failed)
			}
			same, err := service.Prepare(ctx, resource.ID, resource.CurrentCommit, "brief.pdf", false)
			if err != nil || same.ID != d.ID || calls.Load() != 1 {
				t.Fatal("ordinary lookup retried reader")
			}
			retry, err := service.Prepare(ctx, resource.ID, resource.CurrentCommit, "brief.pdf", true)
			if err != nil || retry.ID == d.ID {
				t.Fatal("retry must append artifact")
			}
			waitDocument(t, store, retry.ID, test.status)
			if calls.Load() != 2 {
				t.Fatal("retry call count", calls.Load())
			}
		})
	}
}
func TestPreparationSanitizesUnexpectedReaderFailure(t *testing.T) {
	store, files, resource, _ := documentFixture(t)
	ctx := context.Background()
	var calls atomic.Int32
	service, err := New(ctx, store, files, extractFunc(func(context.Context, string, []byte) (documenttext.Result, error) {
		calls.Add(1)
		return documenttext.Result{}, errors.New("untrusted internal details")
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Shutdown(ctx)
	d, err := service.Prepare(ctx, resource.ID, resource.CurrentCommit, "brief.pdf", false)
	if err != nil {
		t.Fatal(err)
	}
	done := waitDocument(t, store, d.ID, "failed")
	if done.Error.Code != "reader_failed" || done.Error.Message == "untrusted internal details" || calls.Load() != 1 {
		t.Fatal("internal errors leaked or extraction replayed", done)
	}
}

func TestPreparationWriteFailureRetriesResultWithoutReextracting(t *testing.T) {
	store, files, resource, filename := documentFixture(t)
	ctx := context.Background()
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER reject_document_result BEFORE UPDATE OF status ON documents WHEN NEW.status='ready' BEGIN SELECT RAISE(FAIL,'injected document write failure'); END`); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	service, err := New(ctx, store, files, extractFunc(func(context.Context, string, []byte) (documenttext.Result, error) {
		calls.Add(1)
		return documenttext.Result{Format: "pdf", Sections: []documenttext.Section{{ID: "p1", Label: "Page 1", Text: "Durable result"}}}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Shutdown(ctx)
	d, err := service.Prepare(ctx, resource.ID, resource.CurrentCommit, "brief.pdf", false)
	if err != nil {
		t.Fatal(err)
	}
	waitDocument(t, store, d.ID, "extracting")
	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("reader call count %d", calls.Load())
	}
	if _, err := db.Exec(`DROP TRIGGER reject_document_result`); err != nil {
		t.Fatal(err)
	}
	result := waitDocument(t, store, d.ID, "ready")
	if calls.Load() != 1 || result.Sections[0].Text != "Durable result" {
		t.Fatal("write retry reran reader or lost output")
	}
}

func TestRestartDoesNotMislabelAnOlderReaderJob(t *testing.T) {
	store, files, resource, _ := documentFixture(t)
	ctx := context.Background()
	old, err := store.EnsureDocument(ctx, resource.ID, resource.CurrentCommit, "brief.pdf", "previous-reader-version", false)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	service, err := New(ctx, store, files, extractFunc(func(context.Context, string, []byte) (documenttext.Result, error) {
		calls.Add(1)
		return documenttext.Result{}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Shutdown(ctx)
	failed := waitDocument(t, store, old.ID, "unavailable")
	if calls.Load() != 0 || !failed.Error.Retryable || failed.ExtractorVersion != "previous-reader-version" {
		t.Fatal("older job silently used a new parser", failed, calls.Load())
	}
	fresh, err := service.Prepare(ctx, resource.ID, resource.CurrentCommit, "brief.pdf", false)
	if err != nil || fresh.ID == old.ID || fresh.ExtractorVersion != documenttext.Version {
		t.Fatalf("new reader generation %+v %v", fresh, err)
	}
}
