package runs

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/layerfs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func writeTestExport(t *testing.T, directory string) {
	t.Helper()
	archive, err := os.Create(filepath.Join(directory, "result.tar"))
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(archive)
	source := filepath.Join(directory, "project")
	err = filepath.WalkDir(source, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, _ := filepath.Rel(source, name)
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		if err := writer.WriteHeader(&tar.Header{Name: filepath.ToSlash(relative), Mode: 0600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = writer.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive.Name())
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	receipt, _ := json.Marshal(layerfs.ExportedArchive{ArchivePath: archive.Name(), Bytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
	if err := os.WriteFile(filepath.Join(directory, "result.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmedArchiveRejectsPartialOrChangedExport(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "result.tar"), []byte("unfinished"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := confirmedArchive(directory)
	if err != nil || file != nil {
		t.Fatalf("unconfirmed export recovered: %v %v", file, err)
	}
	if err := os.Mkdir(filepath.Join(directory, "project"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "project", "note.md"), []byte("confirmed"), 0600); err != nil {
		t.Fatal(err)
	}
	writeTestExport(t, directory)
	file, err = confirmedArchive(directory)
	if err != nil || file == nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, file)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(directory, "result.tar"))
	data[512] = 'X'
	if err := os.WriteFile(filepath.Join(directory, "result.tar"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if file, err := confirmedArchive(directory); err == nil || file != nil {
		t.Fatal("changed archive accepted")
	}
}

func TestPreparedDependenciesPreserveLocalLinksAndRejectEscapes(t *testing.T) {
	source, project := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "module.js"), []byte("module"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("module.js", filepath.Join(source, "local")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(project, "node_modules")
	if err := copyDependencies(context.Background(), source, target, project); err != nil {
		t.Fatal(err)
	}
	if link, err := os.Readlink(filepath.Join(target, "local")); err != nil || link != "module.js" {
		t.Fatalf("link=%q %v", link, err)
	}
	escape := t.TempDir()
	if err := os.Symlink("../../outside", filepath.Join(escape, "link")); err != nil {
		t.Fatal(err)
	}
	if err := copyDependencies(context.Background(), escape, filepath.Join(project, "bad"), project); err == nil {
		t.Fatal("escaping dependency accepted")
	}
}

func TestManualWriterSharesAgentLeaseWithoutBlockingReaders(t *testing.T) {
	m, s, _, room := testManager(t)
	reader := newAgent(t, s, room, "reader-manual", "read")
	started := make(chan storage.Run, 2)
	release := make(chan struct{})
	m.mu.Lock()
	m.unavailable = ""
	m.executeRun = func(ctx context.Context, e *execution) {
		m.mu.Lock()
		snapshot := e.run
		m.mu.Unlock()
		started <- snapshot
		select {
		case <-release:
		case <-ctx.Done():
		}
		m.finish(e, ctx, nil, "", ctx.Err())
	}
	m.mu.Unlock()
	held := make(chan struct{})
	finishManual := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- m.WithResourceWriter(context.Background(), room.ResourceID, func() error { close(held); <-finishManual; return nil })
	}()
	<-held
	if err := m.WithResourceWriter(context.Background(), room.ResourceID, func() error { t.Error("second manual writer ran"); return nil }); err == nil {
		t.Fatal("manual writer overlap accepted")
	}
	writer := sendTo(t, m, room, "wait-manual", room.DefaultAgentID).Runs[0]
	read := sendTo(t, m, room, "read-during-manual", reader.ID).Runs[0]
	if got := awaitStart(t, started); got.ID != read.ID {
		t.Fatal("reader was blocked by manual file editing")
	}
	noStart(t, started)
	close(finishManual)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if got := awaitStart(t, started); got.ID != writer.ID {
		t.Fatal("writer did not resume after manual save")
	}
	if err := m.WithResourceWriter(context.Background(), room.ResourceID, func() error { t.Error("manual write overlapped agent"); return nil }); err == nil {
		t.Fatal("agent writer lease was ignored")
	}
	close(release)
	waitRun(t, m, writer.ID)
	waitRun(t, m, read.ID)
}

func TestPreparedCacheRetainsActiveAndBoundsIdleHistory(t *testing.T) {
	m := &Manager{layerProjects: map[string]*preparedUsage{
		"active": {active: 2, used: 1, completed: maxPreparedUses},
		"old":    {used: 2}, "warm": {used: 3}, "latest": {used: 4},
	}}
	removed := []string{}
	err := m.prunePreparedProjects(context.Background(), func(_ context.Context, id string) error { removed = append(removed, id); return nil })
	if err != nil || len(removed) != 1 || removed[0] != "old" || m.layerProjects["active"] == nil {
		t.Fatal("cache evicted active or recent work", removed, err)
	}
	m.layerProjects["active"].active = 0
	err = m.prunePreparedProjects(context.Background(), func(_ context.Context, id string) error { return errors.New("cleanup failed") })
	if err == nil || m.layerProjects["active"] == nil {
		t.Fatal("failed cleanup was treated as eviction")
	}
	err = m.prunePreparedProjects(context.Background(), func(_ context.Context, id string) error { removed = append(removed, id); return nil })
	if err != nil || len(removed) != 2 || removed[1] != "active" {
		t.Fatal("completed branch history was retained indefinitely", removed, err)
	}
}
