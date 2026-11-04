package serverstorage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/serviceowner"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func fixture(t *testing.T) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	owner, err := serviceowner.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(root, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	files := resources.New(filepath.Join(root, "resources"))
	source := t.TempDir()
	if err = os.WriteFile(filepath.Join(source, "original.txt"), []byte("Preserve this exact original."), 0644); err != nil {
		t.Fatal(err)
	}
	registered, orphan := strings.Repeat("a", 32), strings.Repeat("b", 32)
	imported, err := files.Import(ctx, registered, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SeedRoom(ctx, storage.Resource{ID: registered, Name: "Retained", CurrentCommit: imported.Commit}); err != nil {
		t.Fatal(err)
	}
	if _, err = files.Import(ctx, orphan, source); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = owner.Close(); err != nil {
		t.Fatal(err)
	}
	return root, registered, orphan
}
func digestTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		relative, _ := filepath.Rel(root, name)
		out[relative] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestInspectAndVerifyAreReadOnlyAndProtectRegisteredVersions(t *testing.T) {
	root, registered, orphan := fixture(t)
	before := digestTree(t, root)
	report, err := Inspect(context.Background(), root, true)
	if err != nil || !report.Complete || !report.Verified || len(report.Objects) == 0 {
		t.Fatal(report, err)
	}
	data, _ := json.Marshal(report)
	if strings.Contains(string(data), "Preserve this exact original") {
		t.Fatal("Inventory exposed original contents")
	}
	found := map[string]string{}
	for _, object := range report.Objects {
		found[object.Path] = object.Retention
	}
	if found["resources/"+registered] != "protected" || found["resources/"+orphan] != "orphan" {
		t.Fatal(found)
	}
	after := digestTree(t, root)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("Read-only inspection changed the original data")
	}
}
func TestMaintenanceQuarantineRestoreAndPermanentPurge(t *testing.T) {
	ctx := context.Background()
	root, registered, orphan := fixture(t)
	before := digestTree(t, filepath.Join(root, "resources", registered))
	plan, err := Plan(ctx, root, 50)
	if err != nil {
		t.Fatal(err)
	}
	state, err := Apply(ctx, root, plan.ID, "quarantine", false)
	if err != nil || state.Status != "quarantined" {
		t.Fatal(state, err)
	}
	if _, err = os.Stat(filepath.Join(root, "resources", orphan)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Orphan was not moved", err)
	}
	state, err = Apply(ctx, root, plan.ID, "quarantine", false)
	if err != nil || state.Status != "quarantined" {
		t.Fatal("Lost reply did not replay", state, err)
	}
	state, err = Apply(ctx, root, plan.ID, "restore", false)
	if err != nil || state.Status != "restored" {
		t.Fatal(state, err)
	}
	if _, err = os.Stat(filepath.Join(root, "resources", orphan)); err != nil {
		t.Fatal(err)
	}
	plan, err = Plan(ctx, root, 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(ctx, root, plan.ID, "quarantine", false); err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(ctx, root, plan.ID, "purge", false); err == nil {
		t.Fatal("Purged without explicit permanent confirmation")
	}
	state, err = Apply(ctx, root, plan.ID, "purge", true)
	if err != nil || state.Status != "purged" {
		t.Fatal(state, err)
	}
	if _, err = os.Stat(filepath.Join(root, quarantined(plan.ID, "resources/"+orphan))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Purge retained the allowed object", err)
	}
	after := digestTree(t, filepath.Join(root, "resources", registered))
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("Maintenance modified a registered original")
	}
	if report, err := Inspect(ctx, root, true); err != nil || !report.Verified {
		t.Fatal(report, err)
	}
}
func TestMaintenanceRejectsActiveOwnerChangedDataAndLinks(t *testing.T) {
	for _, change := range []string{"owner", "content", "reference", "link", "manifest", "foreign-server"} {
		t.Run(change, func(t *testing.T) {
			root, _, orphan := fixture(t)
			ctx := context.Background()
			plan, err := Plan(ctx, root, 50)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "owner":
				owner, err := serviceowner.Acquire(root)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
			case "content":
				if err = os.WriteFile(filepath.Join(root, "resources", orphan, "unexpected"), []byte("new bytes"), 0600); err != nil {
					t.Fatal(err)
				}
			case "reference":
				db, err := sql.Open("sqlite", filepath.Join(root, "desktop.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(`UPDATE team_profile SET name='changed'`)
				if err != nil {
					t.Fatal(err)
				}
				db.Close()
			case "link":
				if err = os.Symlink(t.TempDir(), filepath.Join(root, "unsupported-link")); err != nil {
					t.Fatal(err)
				}
			case "manifest":
				name := filepath.Join(root, maintenanceRoot, "plans", plan.ID, "manifest.json")
				data, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				data = []byte(strings.ReplaceAll(string(data), orphan, strings.Repeat("a", 32)))
				if err = os.WriteFile(name, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "foreign-server":
				db, err := sql.Open("sqlite", filepath.Join(root, "desktop.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(`UPDATE server_identity SET value=?`, strings.Repeat("f", 32))
				if err != nil {
					t.Fatal(err)
				}
				db.Close()
			}
			if _, err = Apply(ctx, root, plan.ID, "quarantine", false); err == nil {
				t.Fatal("Changed authority or bytes were accepted")
			}
			if _, err = os.Stat(filepath.Join(root, "resources", orphan)); err != nil {
				t.Fatal("Refused maintenance still moved data", err)
			}
		})
	}
}
func TestMaintenanceReconcilesRenameAndPartialPermanentPurge(t *testing.T) {
	ctx := context.Background()
	root, _, orphan := fixture(t)
	plan, err := Plan(ctx, root, 50)
	if err != nil {
		t.Fatal(err)
	}
	s, err := open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	manifest, state, err := s.load(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	state.Status = "quarantining"
	if err = s.saveState(state); err != nil {
		t.Fatal(err)
	}
	entry := manifest.Entries[0]
	if err = s.move(ctx, entry.Path, quarantined(plan.ID, entry.Path), entry.Files); err != nil {
		t.Fatal(err)
	}
	// A crash after rename but before the completion receipt must not duplicate it.
	s.close()
	state, err = Apply(ctx, root, plan.ID, "quarantine", false)
	if err != nil || state.Status != "quarantined" {
		t.Fatal(state, err)
	}
	s, err = open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	state.Status = "purging"
	if err = s.saveState(state); err != nil {
		t.Fatal(err)
	}
	removed := false
	for _, file := range entry.Files {
		if !file.Directory {
			if err = s.root.Remove(quarantined(plan.ID, entry.Path) + "/" + file.Path); err != nil {
				t.Fatal(err)
			}
			removed = true
			break
		}
	}
	if !removed {
		t.Fatal("Fixture had no purge bytes")
	}
	s.close()
	if _, err = Apply(ctx, root, plan.ID, "cancel", false); err == nil {
		t.Fatal("Dispatched purge was cancelled")
	}
	state, err = Apply(ctx, root, plan.ID, "purge", true)
	if err != nil || state.Status != "purged" {
		t.Fatal(state, err)
	}
	if _, err = os.Stat(filepath.Join(root, quarantined(plan.ID, "resources/"+orphan))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
func TestInspectionIncludesCommittedWALWithoutCheckpointingSource(t *testing.T) {
	root, _, _ := fixture(t)
	db, err := sql.Open("sqlite", filepath.Join(root, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO runners(id,name,credential_hash,storage_id,created_at,updated_at) VALUES(?,?,?,?,?,?)`, strings.Repeat("d", 32), "Retained Runner", strings.Repeat("e", 64), "retained-storage", "now", "now"); err != nil {
		t.Fatal(err)
	}
	before := digestTree(t, root)
	report, err := Inspect(context.Background(), root, false)
	if err != nil || report.Catalog.Runners != 1 {
		t.Fatal("The WAL reference state was lost", report, err)
	}
	after := digestTree(t, root)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("Inspection checkpointed or modified the source WAL")
	}
}
func copyBackup(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(source, name)
		out := filepath.Join(target, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(out, info.Mode().Perm())
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		return os.WriteFile(out, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestStoppedBackupRetainsOriginalsAndPortableQuarantineRecovery(t *testing.T) {
	ctx := context.Background()
	root, _, orphan := fixture(t)
	plan, err := Plan(ctx, root, 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(ctx, root, plan.ID, "quarantine", false); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	copyBackup(t, root, restored)
	state, err := Apply(ctx, restored, plan.ID, "restore", false)
	if err != nil || state.Status != "restored" {
		t.Fatal(state, err)
	}
	if _, err = os.Stat(filepath.Join(restored, "resources", orphan)); err != nil {
		t.Fatal(err)
	}
	if report, err := Inspect(ctx, restored, true); err != nil || !report.Verified {
		t.Fatal(report, err)
	}
	// Restore never changes the original stopped backup.
	if _, err = os.Stat(filepath.Join(root, quarantined(plan.ID, "resources/"+orphan))); err != nil {
		t.Fatal(err)
	}
}

func TestRegisteredObjectsRemainProtectedEvenWithAValidSealedManifest(t *testing.T) {
	ctx := context.Background()
	root, _, orphan := fixture(t)
	plan, err := Plan(ctx, root, 50)
	if err != nil {
		t.Fatal(err)
	}
	session, err := open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := session.load(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	session.close()
	db, err := sql.Open("sqlite", filepath.Join(root, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO resources(id,name,current_commit,environment,created_at,updated_at) SELECT ?,'Newly registered',current_commit,'{}','now','now' FROM resources LIMIT 1`, orphan)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	session, err = open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ReferenceDigest = session.digest
	id, data, err := fingerprint(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = session.atomicWrite(maintenanceRoot+"/plans/"+id+"/manifest.json", data); err != nil {
		t.Fatal(err)
	}
	if err = session.saveState(State{ID: id, Status: "planned", Completed: []string{}, UpdatedAt: now()}); err != nil {
		t.Fatal(err)
	}
	session.close()
	if _, err = Apply(ctx, root, id, "quarantine", false); err == nil {
		t.Fatal("A sealed plan overrode current reference authority")
	}
	if _, err = os.Stat(filepath.Join(root, "resources", orphan)); err != nil {
		t.Fatal(err)
	}
}
func TestPrivateConfigurationIsNotLoadedAndPurgeCannotRestore(t *testing.T) {
	ctx := context.Background()
	root, _, orphan := fixture(t)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("unparseable private settings must not be loaded"), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(ctx, root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(ctx, root, plan.ID, "quarantine", false); err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(ctx, root, plan.ID, "purge", true); err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(ctx, root, plan.ID, "restore", false); err == nil {
		t.Fatal("A permanent purge pretended to restore")
	}
	replay, err := Apply(ctx, root, plan.ID, "purge", true)
	if err != nil || replay.Status != "purged" {
		t.Fatal(replay, err)
	}
	if _, err = os.Stat(filepath.Join(root, "resources", orphan)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
func TestCancelledPartialQuarantineAndUnexpectedPurgeBytesAreRetained(t *testing.T) {
	ctx := context.Background()
	root, _, orphan := fixture(t)
	plan, err := Plan(ctx, root, 50)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err = Apply(cancelled, root, plan.ID, "quarantine", false); err == nil {
		t.Fatal("Cancelled work succeeded")
	}
	state, err := Apply(ctx, root, plan.ID, "cancel", false)
	if err != nil || state.Status != "cancelled" {
		t.Fatal(state, err)
	}
	if _, err = os.Stat(filepath.Join(root, "resources", orphan)); err != nil {
		t.Fatal(err)
	}
	plan, err = Plan(ctx, root, 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(ctx, root, plan.ID, "quarantine", false); err != nil {
		t.Fatal(err)
	}
	unexpected := filepath.Join(root, quarantined(plan.ID, "resources/"+orphan), "unexpected")
	if err = os.WriteFile(unexpected, []byte("Retain new bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(ctx, root, plan.ID, "purge", true); err == nil {
		t.Fatal("Purged unknown files")
	}
	if data, err := os.ReadFile(unexpected); err != nil || string(data) != "Retain new bytes" {
		t.Fatal(err)
	}
}

func TestInspectionRejectsIncompleteMigrationHistoryWithoutChangingData(t *testing.T) {
	root, _, _ := fixture(t)
	database, err := sql.Open("sqlite", filepath.Join(root, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec("UPDATE goose_db_version SET is_applied=0 WHERE version_id=17"); err != nil {
		t.Fatal(err)
	}
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	before := digestTree(t, root)
	if _, err = Inspect(context.Background(), root, false); err == nil {
		t.Fatal("Incomplete migration history authorized inspection")
	}
	after := digestTree(t, root)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("Failed inspection changed source bytes")
	}
	owner, err := serviceowner.AcquireExisting(root)
	if err != nil {
		t.Fatal("Failed inspection retained ownership", err)
	}
	owner.Close()
}

func TestManifestCannotNameAnObjectParent(t *testing.T) {
	root, _, _ := fixture(t)
	ctx := context.Background()
	plan, err := Plan(ctx, root, 50)
	if err != nil {
		t.Fatal(err)
	}
	session, err := open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer session.close()
	manifest, _, err := session.load(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Entries[0].Files = append(manifest.Entries[0].Files, File{Path: "..", Directory: true, Mode: 0700})
	id, data, err := fingerprint(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = session.atomicWrite(maintenanceRoot+"/plans/"+id+"/manifest.json", data); err != nil {
		t.Fatal(err)
	}
	if err = session.saveState(State{ID: id, Status: "purging", Completed: []string{}, UpdatedAt: now()}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = session.load(id); err == nil {
		t.Fatal("A sealed manifest named its parent")
	}
}
