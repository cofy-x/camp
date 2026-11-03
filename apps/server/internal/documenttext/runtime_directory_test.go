package documenttext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReaderRuntimeCannotExposeServerOrJobAncestors(t *testing.T) {
	root := t.TempDir()
	server := filepath.Join(root, "ServerData")
	jobs := filepath.Join(server, "reader-jobs")
	child := filepath.Join(server, "separate-runtime")
	outside := filepath.Join(root, "independent-runtime")
	for _, name := range []string{root, server, jobs, child, outside} {
		if err := os.MkdirAll(name, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(name, "extract.mjs"), []byte("controlled runtime"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name    string
		allowed bool
	}{{root, false}, {server, false}, {jobs, false}, {child, true}, {outside, true}} {
		_, err := readerRuntimeDirectory(Config{RuntimeDir: test.name, JobRoot: jobs})
		if (err == nil) != test.allowed {
			t.Fatalf("runtime %q allowed=%v: %v", test.name, test.allowed, err)
		}
	}
	alias := filepath.Join(root, strings.ToUpper(filepath.Base(server)))
	if _, err := os.Stat(alias); err == nil {
		if _, err := readerRuntimeDirectory(Config{RuntimeDir: alias, JobRoot: jobs}); err == nil {
			t.Fatal("case alias bypassed private Server containment")
		}
	}
}
