package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStorageCommandsValidateWithoutLoadingConfigurationOrStartingServices(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CAMP_ENV_FILE", filepath.Join(root, "must-not-read.env"))
	var output bytes.Buffer
	if err := Run(context.Background(), []string{"storage", "help"}, &output, "test"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte("--confirm-permanent")) {
		t.Fatal(output.String())
	}
	for _, args := range [][]string{{"storage", "inspect"}, {"storage", "plan", "--data-dir", root, "--limit", "0"}, {"storage", "purge", "--data-dir", root, "--plan", "invalid"}} {
		output.Reset()
		if err := Run(context.Background(), args, &output, "test"); err == nil {
			t.Fatal("Invalid maintenance command succeeded", args)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("Invalid maintenance initialized storage", entries, err)
	}
}
