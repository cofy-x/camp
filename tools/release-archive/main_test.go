package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestArchiveRelocationMetadataAndEscapingLinks(t *testing.T) {
	var previous []byte
	for index := range 2 {
		root := t.TempDir()
		tree := filepath.Join(root, "camp-runtime-darwin-arm64")
		if err := os.MkdirAll(filepath.Join(tree, "bin"), 0700); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(tree, "bin", "tool")
		if err := os.WriteFile(file, []byte("native tool\x00binary"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, time.Now(), time.Unix(int64(index+1), 0)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("tool", filepath.Join(tree, "bin", "alias")); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(root, "runtime.tar.gz")
		if err := archive(tree, output); err != nil {
			t.Fatal(err)
		}
		current, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		if index > 0 && !bytes.Equal(previous, current) {
			t.Fatal("archive identity changed with location or file timestamps")
		}
		previous = current
		if err := archive(tree, output); err == nil {
			t.Fatal("archive overwrote an existing destination")
		}
		if err := os.Symlink(root, filepath.Join(tree, "escape")); err != nil {
			t.Fatal(err)
		}
		failed := filepath.Join(root, "unsafe.tar.gz")
		if err := archive(tree, failed); err == nil {
			t.Fatal("escaping link was accepted")
		}
		if _, err := os.Stat(failed); !os.IsNotExist(err) {
			t.Fatal("failed archive was published", err)
		}
	}
}
