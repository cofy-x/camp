//go:build !darwin

package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyDocumentRecoveryLeavesNonMacStateUntouched(t *testing.T) {
	data := t.TempDir()
	recoverLegacyDocumentJobs(context.Background(), data, nil)
	root := filepath.Join(data, "document-jobs")
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("recovery created reader storage: %v", err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "unrecognized-retained-state")
	if err := os.WriteFile(name, []byte("historical receipt"), 0600); err != nil {
		t.Fatal(err)
	}
	recoverLegacyDocumentJobs(context.Background(), data, nil)
	content, err := os.ReadFile(name)
	if err != nil || string(content) != "historical receipt" {
		t.Fatalf("non-Mac recovery changed retained evidence: %s %v", content, err)
	}
}
