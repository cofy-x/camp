package configuration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFeishuCredentialsPreservePrivateImmutablePairs(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "feishu")
	credentials, err := OpenFeishuCredentials(directory)
	if err != nil {
		t.Fatal(err)
	}
	token, key := "synthetic-verification-token", "synthetic-encryption-key"
	ref, err := credentials.Write(token, key)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := credentials.Write(token+"-new", key+"-new")
	if err != nil || rotated == ref {
		t.Fatal("rotation replaced immutable identity", err)
	}
	reopened, err := OpenFeishuCredentials(directory)
	if err != nil {
		t.Fatal(err)
	}
	value, err := reopened.Read(ref)
	if err != nil || value.VerificationToken != token || value.EncryptKey != key {
		t.Fatal("lost original credential", err)
	}
	raw, _ := json.Marshal(value)
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), key) || string(raw) != "{}" {
		t.Fatal("serialized private credential")
	}
	info, err := os.Stat(filepath.Join(directory, ref+".json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential file not private", err)
	}
	for _, bad := range []string{"../" + ref, strings.Repeat("g", 48), ref + ".json"} {
		if _, err := reopened.Read(bad); err == nil {
			t.Fatal("accepted invalid reference")
		}
	}
	if err := os.Chmod(filepath.Join(directory, ref+".json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Read(ref); err == nil {
		t.Fatal("accepted public credential file")
	}
	for _, bad := range []string{"", "short", strings.Repeat("s", 257), token + "\n", token + "\x00", token + " "} {
		if _, err := credentials.Write(bad, key); err == nil {
			t.Fatal("accepted invalid secret")
		}
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(directory, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFeishuCredentials(link); err == nil {
		t.Fatal("accepted symlink credential directory")
	}
}
