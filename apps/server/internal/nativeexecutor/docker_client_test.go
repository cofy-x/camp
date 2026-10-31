package nativeexecutor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelectedDockerClientPreservesMulticallInvocationName(t *testing.T) {
	root := t.TempDir()
	target, alias := filepath.Join(root, "docker-tools"), filepath.Join(root, "docker")
	if err := os.WriteFile(target, []byte("#!/bin/sh\ntest \"${0##*/}\" = docker\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("docker-tools", alias); err != nil {
		t.Fatal(err)
	}
	selected, err := SelectedDockerClient(alias)
	if err != nil || selected != alias {
		t.Fatal("the selected invocation alias was changed", selected, err)
	}
	if err := exec.Command(selected).Run(); err != nil {
		t.Fatal("multicall dispatch failed", err)
	}
}
