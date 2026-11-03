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
	if err != nil || len(entries) != Files {
		t.Fatal(entries, err)
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
