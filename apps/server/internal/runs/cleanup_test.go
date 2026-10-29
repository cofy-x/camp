//go:build linux

package runs

import (
	"context"
	"github.com/cofy-x/camp/apps/server/internal/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestartRemovesOnlyReservedScratchAfterAcquiringOwnership(t *testing.T) {
	ctx := context.Background()
	manager, store, dir, room := testManager(t)
	turn, err := store.SendMessage(ctx, room.ID, storage.SendMessageInput{RecipientAgentIDs: []string{room.DefaultAgentID}, RequestID: "cleanup", Content: "Keep this execution"}, storage.Budget{MaxTokens: 1024, MaxSteps: 4, TimeoutSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	run := turn.Runs[0]
	runID, projectID, key := run.ID, strings.Repeat("b", 32), strings.Repeat("c", 64)
	removed := []string{
		"resources/.import-abandoned/staging-data",
		"runs/" + runID + "/.references-abandoned/1/brief.md",
		"runs/.probe-abandoned/probe", "runs/.inspect-abandoned/project/package.json",
		"runs/" + runID + "/.verification-abandoned/project/check-output",
		"environments/" + projectID + "/.prepare-abandoned/project/package.json",
	}
	retained := []string{
		"runs/" + runID + "/project/source.js", "runs/" + runID + "/project/.verification-user/keep.txt",
		"runs/" + runID + "/acceptance.mjs", "runs/" + runID + "/runtime.patch.yml",
		"environments/" + projectID + "/" + key + "/ready",
		"environments/" + projectID + "/" + key + "/project/node_modules/package/index.js",
		"environments/" + projectID + "/store/files/package-data",
		"resources/" + projectID + "/objects/keep-result",
	}
	for _, relative := range append(append([]string{}, removed...), retained...) {
		name := filepath.Join(dir, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("Retain original contents"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, ".verification-untouched")
	if err := os.WriteFile(outsideFile, []byte("Outside target"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "runs", strings.Repeat("d", 32))
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	scratchLink := filepath.Join(dir, "environments", projectID, ".prepare-linked")
	if err := os.Symlink(outside, scratchLink); err != nil {
		t.Fatal(err)
	}
	if other, err := New(ctx, store, dir, Config{}, nil); err == nil {
		other.Shutdown(ctx)
		t.Fatal("A second owner acquired the execution directory")
	}
	for _, relative := range removed {
		if _, err := os.Stat(filepath.Join(dir, relative)); err != nil {
			t.Fatalf("Cleanup ran before ownership was acquired: %s %v", relative, err)
		}
	}
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(ctx, store, dir, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown(ctx)
	for _, relative := range removed {
		if _, err := os.Stat(filepath.Join(dir, relative)); !os.IsNotExist(err) {
			t.Fatalf("Scratch survived restart: %s %v", relative, err)
		}
	}
	for _, relative := range retained {
		content, err := os.ReadFile(filepath.Join(dir, relative))
		if err != nil || string(content) != "Retain original contents" {
			t.Fatalf("Restart changed retained data: %s %q %v", relative, content, err)
		}
	}
	if _, err := os.Lstat(scratchLink); !os.IsNotExist(err) {
		t.Fatalf("Scratch symlink remained: %v", err)
	}
	if target, err := os.Readlink(link); err != nil || target != outside {
		t.Fatalf("Cleanup followed/replaced unrelated symlink: %q %v", target, err)
	}
	if content, err := os.ReadFile(outsideFile); err != nil || string(content) != "Outside target" {
		t.Fatalf("Cleanup followed a symlink outside owned directories: %q %v", content, err)
	}
}

func TestDurableCompletionPrunesHostScratchAndPreservesNativeState(t *testing.T) {
	m, store, dir, room := testManager(t)
	ctx := context.Background()
	turn, err := store.SendMessage(ctx, room.ID, storage.SendMessageInput{RecipientAgentIDs: []string{room.DefaultAgentID}, RequestID: "terminal-cleanup", Content: "Finish"}, storage.Budget{MaxTokens: 1024, MaxSteps: 4, TimeoutSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.ClaimRun(ctx, turn.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(m.dir, run.ID)
	if err := m.resources.Prepare(ctx, run.ResourceID, run.BaselineCommit, runDir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"project/dependency", "result.tar", "result.json", "runtime.patch.yml"} {
		if err := os.WriteFile(filepath.Join(runDir, name), []byte("scratch"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(dir, "sessions", "persistent", "1", "sessions")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(state, "conversation.jsonl")
	if err := os.WriteFile(log, []byte("native state"), 0600); err != nil {
		t.Fatal(err)
	}
	_, cancel := context.WithCancel(ctx)
	defer cancel()
	e := &execution{run: run, cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	m.active[run.ID] = e
	m.mu.Unlock()
	m.finish(e, ctx, nil, run.BaselineCommit, nil)
	for _, name := range []string{"project", "result.tar", "result.json", "runtime.patch.yml"} {
		if _, err := os.Lstat(filepath.Join(runDir, name)); !os.IsNotExist(err) {
			t.Fatalf("scratch survived durable finish: %s %v", name, err)
		}
	}
	if content, err := os.ReadFile(log); err != nil || string(content) != "native state" {
		t.Fatal("native session was removed", err)
	}
	if saved, err := m.Get(ctx, run.ID); err != nil || saved.Status != "succeeded" {
		t.Fatal("terminal record was not durable", err)
	}
}
