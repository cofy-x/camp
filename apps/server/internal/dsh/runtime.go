package dsh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// NodeBinary is the private interpreter for the DSH process.
func NodeBinary(directory string) string {
	return filepath.Join(directory, "node", "bin", "node")
}

// RuntimeIdentity checks the installed versions and binds native resume to the
// dependency lock and the Camp providers that supply the tool catalog.
func RuntimeIdentity(ctx context.Context, directory string) (string, error) {
	manifest, err := os.ReadFile(filepath.Join(directory, "node_modules", "@deepseek-ai", "dsh", "package.json"))
	var installed struct {
		Version string `json:"version"`
	}
	if err != nil || json.Unmarshal(manifest, &installed) != nil || installed.Version != Version {
		return "", errors.New("Install the pinned DSH " + Version + " runtime")
	}
	binary := exec.CommandContext(ctx, NodeBinary(directory), "--version")
	binary.Env = []string{}
	output, err := binary.Output()
	if err != nil || strings.TrimSpace(string(output)) != "v"+NodeVersion {
		return "", errors.New("Install the private DSH Node " + NodeVersion + " runtime")
	}
	fingerprint := sha256.New()
	_, _ = io.WriteString(fingerprint, NodeVersion+"\x00")
	_, _ = fingerprint.Write(manifest)
	for _, name := range []string{
		"pnpm-lock.yaml",
		"camp-tools/coordination.mjs",
		"camp-tools/discovery.mjs",
		"camp-tools/filesystem.mjs",
		"camp-tools/shell.mjs",
		"camp-tools/task-inputs.mjs",
		"camp-tools/transport.mjs",
	} {
		content, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return "", errors.New("Install the complete DSH runtime and Camp tool providers")
		}
		_, _ = io.WriteString(fingerprint, "\x00"+name+"\x00")
		_, _ = fingerprint.Write(content)
	}
	return hex.EncodeToString(fingerprint.Sum(nil)), nil
}
