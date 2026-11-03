//go:build darwin

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacServiceObservationSeparatesInstallationRunningAndLogin(t *testing.T) {
	data, login := t.TempDir(), t.TempDir()
	label := serviceStatusLabel("runner", data)
	if label != macServiceLabel(data) {
		t.Fatal("Runner status has another service identity")
	}
	missing := func(string) (string, error) { return "Could not find service", errMacServiceNotFound }
	observe := func(read func(string) (string, error)) serviceStatus {
		return observeMacService(data, "runner", login, 501, read)
	}
	if got := observe(missing); got.State != "absent" || got.Installed || got.Loaded || got.LoginEnabled {
		t.Fatal(got)
	}
	content, err := macServicePlist("/private/camp", data)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(data, "service", label+".plist")
	if err = writeMacPlist(filename, content); err != nil {
		t.Fatal(err)
	}
	if got := observe(missing); got.State != "stopped" || !got.Installed || got.Running || got.LoginEnabled {
		t.Fatal(got)
	}
	if err = writeMacPlist(filepath.Join(login, label+".plist"), content); err != nil {
		t.Fatal(err)
	}
	one := func(target string) (string, error) {
		if strings.HasPrefix(target, "gui/") {
			return "job = {\n state = running\n}", nil
		}
		return missing(target)
	}
	if got := observe(one); got.State != "running" || !got.Running || !got.LoginEnabled {
		t.Fatal(got)
	}
	if got := observe(func(string) (string, error) { return "state = running", nil }); got.State != "ambiguous" {
		t.Fatal(got)
	}
	if got := observe(func(string) (string, error) { return "permission denied", errors.New("failure") }); got.State != "unknown" {
		t.Fatal(got)
	}
	if err = os.WriteFile(filename, []byte("unmanaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := observe(one); got.State != "unknown" || got.Installed {
		t.Fatal(got)
	}
}
