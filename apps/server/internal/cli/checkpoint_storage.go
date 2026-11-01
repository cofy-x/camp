package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func runCheckpointStorage(ctx context.Context, directory, node, workerRuntime, runnerID, storageID string, lock *os.File, jsonOutput bool, output io.Writer) error {
	script := filepath.Join(workerRuntime, "storage.mjs")
	if _, err := os.Stat(script); err != nil {
		return errors.New("Install the current Camp Runner runtime to inspect checkpoint storage; a checkpoint worker is optional")
	}
	input, err := json.Marshal(map[string]string{"dataRoot": directory, "runnerId": runnerID, "storageId": storageID})
	if err != nil {
		return err
	}
	var report, diagnostic bytes.Buffer
	cmd := exec.CommandContext(ctx, node, script)
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + directory, "LANG=en_US.UTF-8"}
	if lock != nil {
		cmd.ExtraFiles = []*os.File{lock}
	}
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = &checkpointStorageOutput{writer: &report}
	cmd.Stderr = &checkpointDiagnostic{writer: &diagnostic}
	cmd.WaitDelay = 2 * time.Second
	progressOutput := output
	if jsonOutput {
		progressOutput = io.Discard
	}
	err = installationPhase(ctx, progressOutput, "Inspecting retained checkpoint storage", 0, func(_ *installPhase) error { return cmd.Run() })
	if err != nil {
		return fmt.Errorf("Checkpoint storage inspection failed; retained data is unchanged: %w %s", err, diagnostic.String())
	}
	return writeCheckpointStorage(output, report.Bytes(), jsonOutput)
}

type checkpointStorageOutput struct{ writer *bytes.Buffer }

func (w *checkpointStorageOutput) Write(data []byte) (int, error) {
	if w.writer.Len()+len(data) > 8<<20 {
		return 0, errors.New("Checkpoint storage report exceeds its bounded output")
	}
	return w.writer.Write(data)
}

type checkpointStorageStats struct {
	LogicalBytes   int64  `json:"logicalBytes"`
	AllocatedBytes *int64 `json:"allocatedBytes"`
	Files          int    `json:"files"`
	Directories    int    `json:"directories"`
	Symlinks       int    `json:"symlinks"`
}

func checkpointSize(bytes int64) string {
	if bytes >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", float64(bytes)/(1<<30))
	}
	return installationSize(bytes)
}

func writeCheckpointStorage(output io.Writer, data []byte, jsonOutput bool) error {
	var report struct {
		Schema          int                    `json:"schema"`
		Kind            string                 `json:"kind"`
		Complete        bool                   `json:"complete"`
		Totals          checkpointStorageStats `json:"totals"`
		Operations      int                    `json:"operations"`
		Epochs          int                    `json:"epochs"`
		RecoveryIntents int                    `json:"recoveryIntents"`
		NativeRecovery  struct {
			Count        int   `json:"count"`
			LogicalBytes int64 `json:"logicalBytes"`
		} `json:"nativeRecovery"`
		Retention struct {
			Protected                int   `json:"protected"`
			Unreferenced             int   `json:"unreferenced"`
			Blocked                  int   `json:"blocked"`
			UnreferencedLogicalBytes int64 `json:"unreferencedLogicalBytes"`
			DeletionSupported        bool  `json:"deletionSupported"`
		} `json:"retention"`
		Captures []struct {
			Path         string `json:"path"`
			LogicalBytes int64  `json:"logicalBytes"`
			Disposition  string `json:"disposition"`
		} `json:"captures"`
		Problems []struct {
			Path string `json:"path"`
			Code string `json:"code"`
		} `json:"problems"`
	}
	if err := json.Unmarshal(data, &report); err != nil || report.Schema != 1 || report.Kind != "checkpoint-storage-preview" || report.Retention.DeletionSupported {
		return errors.New("The checkpoint storage runtime returned an incompatible report")
	}
	if jsonOutput {
		if err := json.NewEncoder(output).Encode(json.RawMessage(data)); err != nil {
			return err
		}
	} else {
		state := "complete"
		if !report.Complete {
			state = "partial; retention preview blocked"
		}
		fmt.Fprintf(output, "Checkpoint storage: %s\nLocal logical bytes: %s (%d files, %d directories, %d symlinks)\n", state, checkpointSize(report.Totals.LogicalBytes), report.Totals.Files, report.Totals.Directories, report.Totals.Symlinks)
		if report.Totals.AllocatedBytes != nil {
			fmt.Fprintf(output, "Observed allocated blocks: %s; APFS clone sharing and reclaimable space are unknown\n", checkpointSize(*report.Totals.AllocatedBytes))
		}
		fmt.Fprintf(output, "Retained operations: %d; epochs: %d; reconstruction intents: %d\nNative recovery entries: %d (%s), all protected\n", report.Operations, report.Epochs, report.RecoveryIntents, report.NativeRecovery.Count, checkpointSize(report.NativeRecovery.LogicalBytes))
		fmt.Fprintf(output, "Captures: %d protected, %d unreferenced (%s), %d blocked\n", report.Retention.Protected, report.Retention.Unreferenced, checkpointSize(report.Retention.UnreferencedLogicalBytes), report.Retention.Blocked)
		for i, capture := range report.Captures {
			if i == 20 {
				fmt.Fprintln(output, "Additional capture paths are available with --json.")
				break
			}
			fmt.Fprintf(output, "  %s %s %q\n", capture.Disposition, checkpointSize(capture.LogicalBytes), capture.Path)
		}
		for _, problem := range report.Problems {
			fmt.Fprintf(output, "  Inspection problem: %s %q\n", problem.Code, problem.Path)
		}
		fmt.Fprintln(output, "Linux Store/VM storage: not observed. Unreferenced captures need manual inspection; this metadata preview does not verify their contents.\nNo data is removed, worker started or model request made.")
	}
	if !report.Complete {
		return errors.New("Checkpoint storage inspection is incomplete; retention preview is blocked and retained data is unchanged")
	}
	return nil
}
