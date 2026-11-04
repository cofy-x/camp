package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRuntimeStageBarrierWaitsForEveryEntry(t *testing.T) {
	archive, pin := runtimeArchiveFixture(t)
	var pending atomic.Int32
	var entries atomic.Int32
	var barriers atomic.Int32
	var mu sync.Mutex
	var handles []*os.File
	root := t.TempDir()
	policy := runtimeStagingSync{
		entry: func(file *os.File) error {
			mu.Lock()
			handles = append(handles, file)
			mu.Unlock()
			pending.Add(1)
			defer pending.Add(-1)
			entries.Add(1)
			return syncRuntimeEntry(file)
		},
		stage: func(directory string) error {
			if pending.Load() != 0 || entries.Load() == 0 || directory != root {
				t.Error("stage barrier preceded admitted file/directory synchronization")
			}
			for _, file := range handles {
				if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Error("stage barrier preceded file close", err)
				}
			}
			barriers.Add(1)
			return syncRuntimeStage(directory)
		},
	}
	if err := extractRuntimeSynced(context.Background(), bytes.NewReader(archive), root, pin.UnpackedBytes, pin.target(), nil, runtimeIOConcurrency, policy); err != nil {
		t.Fatal(err)
	}
	if barriers.Load() != 1 {
		t.Fatal("missing or repeated stage barrier", barriers.Load())
	}
	if _, err := verifyPinnedRuntime(root, pin); err != nil {
		t.Fatal("changed runtime bytes or links", err)
	}
}

func TestRuntimeStageBarrierFailureAndCancellationRejectExtraction(t *testing.T) {
	archive, pin := runtimeArchiveFixture(t)
	want := errors.New("fixture full device flush failure")
	for _, failure := range []string{"entry", "barrier", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var barriers atomic.Int32
			root := t.TempDir()
			policy := runtimeStagingSync{
				entry: func(file *os.File) error {
					if failure == "entry" {
						return want
					}
					return syncRuntimeEntry(file)
				},
				stage: func(directory string) error {
					barriers.Add(1)
					if failure == "cancel" {
						cancel()
						return nil
					}
					return want
				},
			}
			err := extractRuntimeSynced(ctx, bytes.NewReader(archive), root, pin.UnpackedBytes, pin.target(), nil, runtimeIOConcurrency, policy)
			expected := want
			if failure == "cancel" {
				expected = context.Canceled
			}
			if !errors.Is(err, expected) {
				t.Fatal("lost synchronization failure", err)
			}
			if failure == "entry" && barriers.Load() != 0 {
				t.Fatal("failed entry reached the completion barrier")
			}
			// Extraction returns only after all owned handles close, even on error.
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, "manifest.json")); !os.IsNotExist(err) {
				t.Fatal(err)
			}
		})
	}
}
