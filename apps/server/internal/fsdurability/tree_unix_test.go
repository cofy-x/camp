//go:build linux || darwin

package fsdurability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "nested/c"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(filepath.Join(root, "a"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/not-granted/opaque", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestTreeClosesBoundedEntriesBeforeOnePinnedFullBarrier(t *testing.T) {
	root := fixture(t)
	var mu sync.Mutex
	var handles []*os.File
	var active atomic.Int32
	var peak atomic.Int32
	var barriers atomic.Int32
	err := syncTree(context.Background(), root, func(file *os.File) error {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); old < n && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		mu.Lock()
		handles = append(handles, file)
		mu.Unlock()
		return Entry(file)
	}, func(file *os.File) error {
		barriers.Add(1)
		if active.Load() != 0 {
			t.Error("full barrier preceded entry completion")
		}
		for _, handle := range handles {
			if _, err := handle.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Error("full barrier preceded handle close", err)
			}
		}
		before, e := os.Stat(root)
		pinned, p := file.Stat()
		if e != nil || p != nil || !os.SameFile(before, pinned) {
			t.Error("full barrier lost the pinned tree root")
		}
		return file.Sync()
	})
	if err != nil {
		t.Fatal(err)
	}
	if peak.Load() > concurrency || barriers.Load() != 1 || len(handles) != 6 {
		t.Fatal("unbounded, missing or repeated synchronization", peak.Load(), barriers.Load(), len(handles))
	}
	if bytes, err := os.ReadFile(filepath.Join(root, "a")); err != nil || string(bytes) != "a" {
		t.Fatal("changed synchronized bytes", err)
	}
	if target, err := os.Readlink(filepath.Join(root, "link")); err != nil || target != "/not-granted/opaque" {
		t.Fatal("changed or followed opaque link", err)
	}
}

func TestTreeRejectsEntryAndBarrierFailuresAndCancellation(t *testing.T) {
	want := errors.New("controlled synchronization failure")
	for _, point := range []string{"entry", "barrier", "cancel"} {
		t.Run(point, func(t *testing.T) {
			root := fixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var barriers atomic.Int32
			err := syncTree(ctx, root, func(file *os.File) error {
				if point == "entry" {
					return want
				}
				return Entry(file)
			}, func(file *os.File) error {
				barriers.Add(1)
				if point == "cancel" {
					cancel()
					return nil
				}
				return want
			})
			expected := want
			if point == "cancel" {
				expected = context.Canceled
			}
			if !errors.Is(err, expected) {
				t.Fatal("lost synchronization failure", err)
			}
			if point == "entry" && barriers.Load() != 0 {
				t.Fatal("failed entry reached full barrier")
			}
			if _, err := os.Stat(filepath.Join(root, "nested/c")); err != nil {
				t.Fatal("failure removed retained staging", err)
			}
		})
	}
}

func TestTreeRefusesUnsupportedEntriesAndRootAliases(t *testing.T) {
	root := fixture(t)
	alias := filepath.Join(t.TempDir(), "root-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if err := Tree(context.Background(), alias); err == nil {
		t.Fatal("followed root symlink")
	}
	if err := syscall.Mkfifo(filepath.Join(root, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Tree(context.Background(), root); err == nil {
		t.Fatal("synchronized unsupported FIFO")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Tree(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestTreeDetectsReplacementBeforeFinalBarrier(t *testing.T) {
	root := fixture(t)
	var replaced atomic.Bool
	err := syncTree(context.Background(), root, func(file *os.File) error {
		if replaced.CompareAndSwap(false, true) {
			return os.WriteFile(filepath.Join(root, "new-file"), []byte("late write"), 0600)
		}
		return Entry(file)
	}, func(*os.File) error { t.Error("changed tree reached final barrier"); return nil })
	if err == nil {
		t.Fatal("late namespace mutation became confirmed synchronization")
	}
}
