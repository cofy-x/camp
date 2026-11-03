package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/documenttext"
)

func TestDocumentRuntimeUsesOnlyProcessConfiguration(t *testing.T) {
	cwd := t.TempDir()
	for _, value := range []string{"", "local-parser", filepath.Join(cwd, "external-parser")} {
		got := documentRuntime(func(key string) string {
			if key != "CAMP_DOCUMENT_RUNTIME" {
				t.Fatalf("unexpected configuration lookup %q", key)
			}
			return value
		}, cwd)
		want := value
		if value == "" {
			want = filepath.Join(cwd, "tools", "document-runtime")
		} else if !filepath.IsAbs(value) {
			want = filepath.Join(cwd, value)
		}
		if got != want {
			t.Fatalf("runtime %q: got %q, want %q", value, got, want)
		}
	}
}

func TestDocumentNodeHasProcessOnlyOwnership(t *testing.T) {
	cwd := t.TempDir()
	node := filepath.Join(cwd, "selected-node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".env"), []byte("CAMP_DOCUMENT_NODE=ignored\nCAMP_DSH_NODE=ignored-legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	config := resolveDocumentSettings(func(key string) string {
		if key == "CAMP_DOCUMENT_NODE" {
			return node
		}
		return ""
	}, cwd, "/source/camp")
	if config.InstallationError != nil || config.NodePath != node || config.HelperPath != "/source/camp-sandbox" {
		t.Fatal("document config inherited model settings or lost its source helper")
	}
}

func TestExplicitDocumentNodeNeverFallsBack(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"node", "selected-node"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", directory)
	for _, config := range []documenttext.Config{{NodePath: filepath.Join(directory, "node")}, {NodePath: "/verified/bundled/node", RuntimeDir: "/verified/bundled/runtime"}} {
		for _, override := range []string{"missing-selected-node", filepath.Join(directory, "missing-selected-node"), "selected-node", "./selected-node"} {
			selected := applyDocumentOverrides(config, func(key string) string {
				if key == "CAMP_DOCUMENT_NODE" {
					return override
				}
				return ""
			}, directory)
			if override == "selected-node" || override == "./selected-node" {
				if selected.InstallationError != nil || selected.NodePath != filepath.Join(directory, "selected-node") {
					t.Fatalf("lost explicit executable %q: %+v", override, selected)
				}
			} else if selected.InstallationError == nil || selected.NodePath != override {
				t.Fatalf("invalid explicit Node silently selected a default: %+v", selected)
			}
		}
	}
}
