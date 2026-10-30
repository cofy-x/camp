package nativejob

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateSpecAcceptsTheToolScriptLimitAndRejectsPublicFiles(t *testing.T) {
	directory := t.TempDir()
	name := filepath.Join(directory, "request.json")
	spec := Spec{Argv: []string{"/bin/sh", "-c", strings.Repeat("x", 128<<10)}, Directory: directory, TimeoutMS: 1000, MaxOutput: 4096}
	if err := save(name, spec); err != nil {
		t.Fatal(err)
	}
	loaded, err := readSpec(name)
	if err != nil || len(loaded.Argv[2]) != 128<<10 {
		t.Fatal("a valid tool script lost its request", err)
	}
	if err := os.Chmod(name, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSpec(name); err == nil {
		t.Fatal("a public job configuration was admitted")
	}
}
