//go:build darwin

package execution

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeStateScopeAddsOnlyItsOwnedBoundaryAndRetainsExistingData(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"unrelated-host-parent","exports":"./private.js"}`), 0600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "private-state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(state, "retained-session")
	if err := os.WriteFile(session, []byte("stable private state"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareDSHStateScope(state); err != nil {
		t.Fatal(err)
	}
	if err := prepareDSHStateScope(state); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(session); err != nil || string(data) != "stable private state" {
		t.Fatal("state boundary replaced retained data", err)
	}
	if err := os.WriteFile(filepath.Join(state, "package.json"), []byte("unexpected retained content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareDSHStateScope(state); err == nil {
		t.Fatal("an existing incompatible file was overwritten")
	}
	if data, _ := os.ReadFile(filepath.Join(state, "package.json")); string(data) != "unexpected retained content" {
		t.Fatal("incompatible boundary was not retained")
	}
}
