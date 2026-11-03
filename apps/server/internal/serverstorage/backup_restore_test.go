package serverstorage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/serviceowner"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

const crashWALFixtureDirectory = "CAMP_TEST_STORAGE_CRASH_WAL_DIRECTORY"
const crashWALTeamName = "Committed team state retained only in WAL"

// Exit without closing SQLite to retain the committed WAL exactly as after a
// process crash. The parent copies these stopped bytes, never a live database.
func TestStoppedWALCrashFixtureHelper(t *testing.T) {
	directory := os.Getenv(crashWALFixtureDirectory)
	if directory == "" {
		t.Skip("subprocess fixture")
	}
	db, err := sql.Open("sqlite", filepath.Join(directory, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE team_profile SET name=? WHERE id=1", crashWALTeamName); err != nil {
		t.Fatal(err)
	}
	var identity string
	if err = db.QueryRow("SELECT value FROM server_identity WHERE id=1").Scan(&identity); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, identity)
	os.Exit(0)
}

func TestStoppedCrashBackupRestartsFromCommittedWALAndRetainsIdentity(t *testing.T) {
	ctx := context.Background()
	root, registered, _ := fixture(t)
	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStoppedWALCrashFixtureHelper$")
	child.Env = append(os.Environ(), crashWALFixtureDirectory+"="+root)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("create stopped crash fixture: %v\n%s", err, output)
	}
	identity := strings.TrimSpace(string(output))
	if !objectID.MatchString(identity) {
		t.Fatalf("crash fixture returned an invalid Server identity: %q", identity)
	}
	if wal, err := os.Stat(filepath.Join(root, "desktop.sqlite-wal")); err != nil || wal.Size() <= 32 {
		t.Fatal("crash fixture did not retain committed WAL frames", err)
	}
	before := digestTree(t, root)
	// A database-only copy must lack the new state, proving the restore really
	// depends on the committed WAL rather than an unnoticed prior checkpoint.
	mainOnly := filepath.Join(t.TempDir(), "main-only.sqlite")
	data, err := os.ReadFile(filepath.Join(root, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(mainOnly, data, 0600); err != nil {
		t.Fatal(err)
	}
	mainDB, err := sql.Open("sqlite", mainOnly)
	if err != nil {
		t.Fatal(err)
	}
	var checkpointedName string
	err = mainDB.QueryRow("SELECT name FROM team_profile WHERE id=1").Scan(&checkpointedName)
	closeErr := mainDB.Close()
	if err != nil || closeErr != nil || checkpointedName == crashWALTeamName {
		t.Fatal("the committed test state was not isolated to WAL", checkpointedName, err, closeErr)
	}

	restored := filepath.Join(t.TempDir(), "restored")
	copyBackup(t, root, restored)
	owner, err := serviceowner.AcquireExisting(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	store, err := storage.Open(filepath.Join(restored, "desktop.sqlite"))
	if err != nil {
		t.Fatal("reopen the restored Server database", err)
	}
	defer store.Close()
	team, err := store.GetTeam(ctx)
	if err != nil || team.Name != crashWALTeamName {
		t.Fatal("Server restart lost business state committed in WAL", team, err)
	}
	resource, err := store.GetResource(ctx, registered)
	if err != nil || resource.ID != registered {
		t.Fatal("Server restart lost the registered Resource", resource, err)
	}
	content, err := resources.New(filepath.Join(restored, "resources")).ReadFile(ctx, registered, resource.CurrentCommit, "original.txt")
	if err != nil || string(content) != "Preserve this exact original." {
		t.Fatal("the restored database no longer agrees with its Git original", err)
	}
	// Read the reopened database itself, not an inspection catalog or its copy.
	reopened, err := sql.Open("sqlite", filepath.Join(restored, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	var restoredIdentity string
	err = reopened.QueryRow("SELECT value FROM server_identity WHERE id=1").Scan(&restoredIdentity)
	closeErr = reopened.Close()
	if err != nil || closeErr != nil || restoredIdentity != identity {
		t.Fatal("Server restart replaced the backup's persistent identity", restoredIdentity, identity, err, closeErr)
	}
	if !reflect.DeepEqual(before, digestTree(t, root)) {
		t.Fatal("restoring and restarting a relocated copy changed the stopped backup")
	}
}
