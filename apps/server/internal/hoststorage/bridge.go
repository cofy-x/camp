// Package hoststorage owns the bounded, credential-free Node storage helper.
package hoststorage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type boundedOutput struct {
	bytes.Buffer
	maximum int
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	if w.Len()+len(p) > w.maximum {
		return 0, errors.New("Storage helper output exceeds its bound")
	}
	return w.Buffer.Write(p)
}

func Call(ctx context.Context, node, runtimeDir, directory string, input any, lock *os.File) (json.RawMessage, error) {
	script := filepath.Join(runtimeDir, "storage.mjs")
	if _, err := os.Stat(script); err != nil {
		return nil, errors.New("Install the current Camp Runner runtime for storage administration; a worker is optional")
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 64<<10 {
		return nil, errors.New("Storage helper input exceeds its bound")
	}
	cmd := exec.CommandContext(ctx, node, script)
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + directory, "LANG=en_US.UTF-8"}
	if lock != nil {
		cmd.ExtraFiles = []*os.File{lock}
	}
	cmd.Stdin = bytes.NewReader(encoded)
	output := boundedOutput{maximum: 8 << 20}
	diagnostic := boundedOutput{maximum: 8192}
	cmd.Stdout = &output
	cmd.Stderr = &diagnostic
	cmd.WaitDelay = 2 * time.Second
	if err = cmd.Run(); err != nil {
		var failure struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(output.Bytes(), &failure) == nil && failure.Error.Code != "" {
			return nil, fmt.Errorf("Storage administration %s: %s", failure.Error.Code, failure.Error.Message)
		}
		return nil, fmt.Errorf("Storage administration failed; inspect the exact retained operation before retrying: %w %s", err, diagnostic.String())
	}
	if !json.Valid(output.Bytes()) {
		return nil, errors.New("Storage helper returned invalid JSON")
	}
	return bytes.Clone(output.Bytes()), nil
}
