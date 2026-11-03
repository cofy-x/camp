package servicelog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBoundedRotationRetainsNewestBytes(t *testing.T) {
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	w, err := open(directory, "runner.log", 8)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("abcdefghijklmnopqrstuvwxyz0123456789")
	if n, err := w.Write(content); err != nil || n != len(content) {
		t.Fatal(n, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var retained []byte
	for i := Files - 1; i >= 0; i-- {
		data, err := os.ReadFile(w.filename(i))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 8 {
			t.Fatal("oversized log")
		}
		retained = append(retained, data...)
	}
	if !bytes.Equal(retained, content[16:]) {
		t.Fatalf("retained %q", retained)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != Files+1 {
		t.Fatal(entries, err)
	}
	if info, err := os.Lstat(w.path + ".lock"); err != nil || info.Mode().Perm() != 0600 || info.Size() != 0 {
		t.Fatal("Missing stable private coordination inode", info, err)
	}
}
func TestLogsRejectAliasesAndPreserveTargets(t *testing.T) {
	for _, hard := range []bool{false, true} {
		t.Run(map[bool]string{true: "hardlink", false: "symlink"}[hard], func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "private")
			if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(root, "runner.log")
			if hard {
				err = os.Link(target, log)
			} else {
				err = os.Symlink(target, log)
			}
			if err != nil {
				t.Fatal(err)
			}
			if w, err := open(root, "runner.log", 8); err == nil {
				w.Close()
				t.Fatal("accepted linked log")
			}
			data, _ := os.ReadFile(target)
			if string(data) != "unchanged" {
				t.Fatal("target changed")
			}
		})
	}
}
func TestOversizedHistoricalLogsAreBounded(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "server.log")
	if err := os.WriteFile(name, []byte("old-prefix-new-tail"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := open(root, "server.log", 8)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(name)
	if string(data) != "new-tail" {
		t.Fatal(string(data))
	}
}

func TestDuplicateWriterCannotChangeActiveBytesOrSizeAccounting(t *testing.T) {
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	w, err := open(directory, "server.log", 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("123456")); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(w.path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := open(directory, "server.log", 8); err == nil {
		duplicate.Close()
		t.Fatal("Concurrent opener took the live stream")
	}
	content, err := os.ReadFile(w.path)
	if err != nil || string(content) != "123456" || w.size != 6 {
		t.Fatal("Failed duplicate changed active log", string(content), w.size, err)
	}
	if _, err = w.Write([]byte("7890")); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := open(directory, "server.log", 8)
	if err != nil {
		t.Fatal("Closed owner did not release stream", err)
	}
	defer next.Close()
	after, err := os.Stat(w.path + ".lock")
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("Coordination inode was replaced", err)
	}
	for i := 0; i < Files; i++ {
		info, err := os.Stat(w.filename(i))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || info.Size() > 8 {
			t.Fatal("Concurrent size accounting exceeded bound", info, err)
		}
	}
}

func TestFailedRotationStillReleasesStreamOwnership(t *testing.T) {
	directory, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	w, err := open(directory, "runner.log", 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("12345678")); err != nil {
		t.Fatal(err)
	}
	if err = w.file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("9")); err == nil {
		t.Fatal("Expected closed payload synchronization failure")
	}
	_ = w.Close()
	next, err := open(directory, "runner.log", 8)
	if err != nil {
		t.Fatal("Failed rotation retained lock after Close", err)
	}
	defer next.Close()
}

func TestLogsRejectLinkedCoordinationFilesWithoutTouchingTargets(t *testing.T) {
	for _, hard := range []bool{false, true} {
		t.Run(map[bool]string{true: "hardlink", false: "symlink"}[hard], func(t *testing.T) {
			directory, _ := filepath.EvalSymlinks(t.TempDir())
			if err := os.Chmod(directory, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(directory, "private")
			if err := os.WriteFile(target, []byte("preserved"), 0600); err != nil {
				t.Fatal(err)
			}
			lock := filepath.Join(directory, "server.log.lock")
			var err error
			if hard {
				err = os.Link(target, lock)
			} else {
				err = os.Symlink(target, lock)
			}
			if err != nil {
				t.Fatal(err)
			}
			if w, err := open(directory, "server.log", 8); err == nil {
				w.Close()
				t.Fatal("Linked coordination inode accepted")
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "preserved" {
				t.Fatal("Lock target changed", err)
			}
			if _, err = os.Lstat(filepath.Join(directory, "server.log")); !os.IsNotExist(err) {
				t.Fatal("Failed ownership probe created log payload", err)
			}
		})
	}
}
