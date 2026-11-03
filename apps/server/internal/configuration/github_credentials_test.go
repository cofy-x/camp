package configuration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubCredentialsPrivateImmutableVersions(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "github")
	credentials, err := OpenGitHubCredentials(directory)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("private-token", 5)
	ref, err := credentials.Write(token, 123)
	if err != nil {
		t.Fatal(err)
	}
	next, err := credentials.Write(token+"-rotated", 124)
	if err != nil || next == ref {
		t.Fatal("rotation did not preserve an independent credential", err)
	}
	reopened, err := OpenGitHubCredentials(directory)
	if err != nil {
		t.Fatal(err)
	}
	value, err := reopened.Read(ref)
	if err != nil || value.Token != token || value.ActorID != 123 {
		t.Fatal("original credential was not preserved", err)
	}
	raw, _ := json.Marshal(value)
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), "token") {
		t.Fatal("credential token escaped through JSON")
	}
	info, err := os.Stat(filepath.Join(directory, ref+".json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions are not private", err)
	}
	for _, bad := range []string{"../" + ref, strings.Repeat("g", 48), ref + ".json"} {
		if _, err = reopened.Read(bad); err == nil {
			t.Fatalf("accepted invalid reference %q", bad)
		}
	}
	if err = os.Chmod(filepath.Join(directory, ref+".json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.Read(ref); err == nil {
		t.Fatal("accepted a public credential file")
	}
}

func TestGitHubCredentialsRejectInvalidInputAndSymlinks(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "github")
	credentials, err := OpenGitHubCredentials(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"short", strings.Repeat("x", 1025), strings.Repeat("x", 30) + "\r\n", strings.Repeat("x", 30) + " ", strings.Repeat("x", 30) + "\x00"} {
		if _, err := credentials.Write(token, 1); err == nil {
			t.Fatal("accepted malformed token")
		}
	}
	if _, err := credentials.Write(strings.Repeat("x", 30), 0); err == nil {
		t.Fatal("accepted missing account identity")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(directory, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenGitHubCredentials(link); err == nil {
		t.Fatal("accepted symlinked credential directory")
	}
}
