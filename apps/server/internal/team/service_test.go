package team

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func testService(t *testing.T) (*Service, *storage.Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, e := storage.Open(filepath.Join(dir, "team.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	service, e := Open(context.Background(), store, dir)
	if e != nil {
		t.Fatal(e)
	}
	return service, store, dir
}
func TestSetupTokenPersistsAndCannotBeReused(t *testing.T) {
	service, store, dir := testService(t)
	ctx := context.Background()
	info, e := os.Stat(service.SetupPath())
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("setup file mode: %v %v", info, e)
	}
	token, e := os.ReadFile(service.SetupPath())
	if e != nil {
		t.Fatal(e)
	}
	second, e := Open(ctx, store, dir)
	if e != nil {
		t.Fatal(e)
	}
	again, e := os.ReadFile(second.SetupPath())
	if e != nil || string(again) != string(token) {
		t.Fatal("restart replaced setup token")
	}
	if _, _, e = service.Setup(ctx, strings.Repeat("x", 43), "owner", "Owner", "a sufficiently long password"); e == nil {
		t.Fatal("invalid setup accepted")
	}
	u, session, e := service.Setup(ctx, strings.TrimSpace(string(token)), "owner", "Owner", "a sufficiently long password")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(service.SetupPath()); !os.IsNotExist(e) {
		t.Fatal("consumed setup file remains")
	}
	if _, _, e = service.Setup(ctx, strings.TrimSpace(string(token)), "second", "Second", "a sufficiently long password"); e == nil {
		t.Fatal("setup replay accepted")
	}
	if current, e := service.Authenticate(ctx, session); e != nil || current.ID != u.ID {
		t.Fatal("created session invalid", e)
	}
	if _, e = Open(ctx, store, dir); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(service.SetupPath()); !os.IsNotExist(e) {
		t.Fatal("restart recreated setup token")
	}
}
func TestPasswordsAreArgon2idAndInvalidatedSessionsStayClosed(t *testing.T) {
	service, _, _ := testService(t)
	ctx := context.Background()
	token, _ := os.ReadFile(service.SetupPath())
	_, session, e := service.Setup(ctx, strings.TrimSpace(string(token)), "owner", "Owner", "a sufficiently long password")
	if e != nil {
		t.Fatal(e)
	}
	hash, e := hashPassword("a sufficiently long password")
	if e != nil || !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatal("password parameters", e)
	}
	if !verifyPassword("a sufficiently long password", hash) || verifyPassword("wrong", hash) || verifyPassword("a sufficiently long password", strings.Replace(hash, "m=65536", "m=1048576", 1)) {
		t.Fatal("password verification")
	}
	stream, stop := service.WatchSession(ctx, session)
	defer stop()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	service.RevalidateSessions(cancelled)
	select {
	case <-stream.Done():
		t.Fatal("cancelled validation revoked a valid session")
	default:
	}
	if e = service.Logout(ctx, session); e != nil {
		t.Fatal(e)
	}
	select {
	case <-stream.Done():
	case <-time.After(time.Second):
		t.Fatal("logout left session stream open")
	}
	if _, e = service.Authenticate(ctx, session); e == nil {
		t.Fatal("revoked session accepted")
	}
}
func TestSetupRefusesPublicOrSymlinkTokenFile(t *testing.T) {
	for _, kind := range []string{"public", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			service, store, dir := testService(t)
			if kind == "public" {
				if e := os.Chmod(service.SetupPath(), 0644); e != nil {
					t.Fatal(e)
				}
			} else {
				bytes, e := os.ReadFile(service.SetupPath())
				if e != nil {
					t.Fatal(e)
				}
				target := filepath.Join(dir, "elsewhere")
				if e = os.WriteFile(target, bytes, 0600); e != nil {
					t.Fatal(e)
				}
				if e = os.Remove(service.SetupPath()); e != nil {
					t.Fatal(e)
				}
				if e = os.Symlink(target, service.SetupPath()); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := Open(context.Background(), store, dir); e == nil {
				t.Fatal("unsafe setup file accepted")
			}
		})
	}
}
