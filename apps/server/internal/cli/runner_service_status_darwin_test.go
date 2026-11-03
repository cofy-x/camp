//go:build darwin

package cli

import (
	"context"
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

func TestMacServiceStatusUsesTheCanonicalStorageIdentity(t *testing.T) {
	directory := t.TempDir()
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "selected-storage")
	if err = os.Symlink(canonical, alias); err != nil {
		t.Fatal(err)
	}
	content, err := macServicePlist("/private/camp", canonical)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeMacPlist(filepath.Join(canonical, "service", macServiceLabel(canonical)+".plist"), content); err != nil {
		t.Fatal(err)
	}
	// Read-only launchctl queries may be unavailable in the test account; the
	// installed fact must still come from the same canonical private directory.
	if got := readMacServiceStatus(context.Background(), alias, "runner"); !got.Installed {
		t.Fatal("An alias selected another service identity", got)
	}
}
