package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckpointImageChecksBeforeDockerLoadAndKeepsArchive(t *testing.T) {
	root := t.TempDir()
	archive, docker, marker := filepath.Join(root, "input.tar.gz"), filepath.Join(root, "docker"), filepath.Join(root, "loaded")
	content := []byte("controlled image fixture")
	if err := os.WriteFile(archive, content, 0600); err != nil {
		t.Fatal(err)
	}
	image := "sha256:" + strings.Repeat("a", 64)
	pin := checkpointImagePin{Image: image, ArchiveSHA256: fmt.Sprintf("%x", sha256.Sum256(content)), ArchiveBytes: int64(len(content))}
	// Inspect is unavailable before the controlled load; afterwards it returns
	// the exact pinned identity. No actual engine or enrolled data is consulted.
	script := fmt.Sprintf("#!/bin/sh\nset -eu\nif [ \"$1 $2\" = 'image load' ]; then test -f \"$4\"; printf loaded > %q; exit 0; fi\nif test -f %q; then printf '[{\"Id\":\"%s\",\"Os\":\"linux\",\"Architecture\":\"arm64\",\"Config\":{\"Labels\":{\"camp.runtime\":\"layerfs-worker\"}}}]'; else exit 1; fi\n", marker, marker, image)
	if err := os.WriteFile(docker, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	bad := pin
	bad.ArchiveSHA256 = strings.Repeat("b", 64)
	if err := loadCheckpointImage(context.Background(), root, archive, docker, "/controlled/docker.sock", bad); err == nil {
		t.Fatal("a corrupt artifact was loaded")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Docker load ran before checksum verification")
	}
	if err := loadCheckpointImage(context.Background(), root, archive, docker, "/controlled/docker.sock", pin); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(archive); err != nil || string(data) != string(content) {
		t.Fatal("the input archive was changed", err)
	}
	if err := loadCheckpointImage(context.Background(), root, "", docker, "/controlled/docker.sock", pin); err != nil {
		t.Fatal("a matching installed image was not reused", err)
	}
	if err := loadCheckpointImage(context.Background(), root, archive, docker, "/controlled/docker.sock", bad); err == nil {
		t.Fatal("an explicit corrupt artifact was ignored because an image existed")
	}
}
