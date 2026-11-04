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

func TestRuntimeCacheVerifiesNormalizedBytesModesAndLinks(t *testing.T) {
	root := t.TempDir()
	tree := filepath.Join(root, "camp-runtime-darwin-arm64")
	if err := os.MkdirAll(filepath.Join(tree, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(tree, "bin", "tool")
	if err := os.WriteFile(file, []byte("original binary"), 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tree, "bin", "alias")
	if err := os.Symlink("tool", link); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, "cache")
	check := func(name string, wantHit bool) []byte {
		t.Helper()
		output := filepath.Join(root, name+".tar.gz")
		hit, err := cachedArchive(tree, output, cache)
		if err != nil || hit != wantHit {
			t.Fatalf("%s: hit=%v, err=%v", name, hit, err)
		}
		plain := filepath.Join(root, name+"-uncached.tar.gz")
		if err := archive(tree, plain); err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := os.ReadFile(plain)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, expected) {
			t.Fatal("cache changed the deterministic archive bytes")
		}
		return actual
	}
	cold := check("cold", false)
	if err := os.Chtimes(file, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cold, check("warm", true)) {
		t.Fatal("timestamps invalidated a normalized artifact")
	}
	if err := os.WriteFile(file, []byte("changed binary"), 0755); err != nil {
		t.Fatal(err)
	}
	check("changed-bytes", false)
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	check("changed-executable", false)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../bin/tool", link); err != nil {
		t.Fatal(err)
	}
	check("changed-link", false)
	if err := os.WriteFile(filepath.Join(cache, "runtime.tar.gz"), []byte("corrupt compressed artifact"), 0644); err != nil {
		t.Fatal(err)
	}
	check("corrupt-cache", false)
	if err := os.Mkdir(filepath.Join(tree, "empty-directory"), 0755); err != nil {
		t.Fatal(err)
	}
	check("changed-directory", false)
	output := filepath.Join(root, "existing.tar.gz")
	if err := os.WriteFile(output, []byte("preserve existing"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := cachedArchive(tree, output, cache); err == nil {
		t.Fatal("cache overwrote an existing destination")
	}
	if actual, _ := os.ReadFile(output); string(actual) != "preserve existing" {
		t.Fatal("existing destination was changed")
	}
	if err := os.Symlink(root, filepath.Join(tree, "escape")); err != nil {
		t.Fatal(err)
	}
	unsafe := filepath.Join(root, "unsafe.tar.gz")
	if _, err := cachedArchive(tree, unsafe, cache); err == nil {
		t.Fatal("cached tree accepted an escaping link")
	}
	if _, err := os.Stat(unsafe); !os.IsNotExist(err) {
		t.Fatal("invalid cached tree was published")
	}
}
