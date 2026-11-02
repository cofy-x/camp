package configuration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebhookSecretPersistenceAndPrivateBoundary(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "credentials")
	secrets, err := OpenWebhookSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("private-", 8)
	ref, err := secrets.Put(secret)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWebhookSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	value, err := reopened.Get(ref)
	if err != nil || value != secret {
		t.Fatal("credential lost across reopen", err)
	}
	if _, err = secrets.Put("short"); err == nil {
		t.Fatal("short credential accepted")
	}
	if _, err = secrets.Get("../" + ref); err == nil {
		t.Fatal("credential path traversal")
	}
	path := filepath.Join(dir, ref+".json")
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = secrets.Get(ref); err == nil {
		t.Fatal("public credential file read")
	}
	link := filepath.Join(t.TempDir(), "linked")
	if err = os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenWebhookSecrets(link); err == nil {
		t.Fatal("symlink credential directory accepted")
	}
}
