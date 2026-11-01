package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cofy-x/camp/apps/server/internal/hoststorage"
)

func runCheckpointStorage(ctx context.Context, directory, node, workerRuntime, runnerID, storageID string, lock *os.File, jsonOutput bool, output io.Writer) error {
	script := filepath.Join(workerRuntime, "storage.mjs")
	if _, err := os.Stat(script); err != nil {
		return errors.New("Install the current Camp Runner runtime to inspect checkpoint storage; a checkpoint worker is optional")
	}
	input := map[string]any{"dataRoot": directory, "runnerId": runnerID, "storageId": storageID, "quiescent": lock != nil}
	var report json.RawMessage
	progressOutput := output
	if jsonOutput {
		progressOutput = io.Discard
	}
	err := installationPhase(ctx, progressOutput, "Inspecting retained checkpoint storage", 0, func(_ *installPhase) error {
		var e error
		report, e = hoststorage.Call(ctx, node, workerRuntime, directory, input, lock)
		return e
	})
	if err != nil {
		return err
	}
	return writeCheckpointStorage(output, report, jsonOutput)
}

func runCheckpointStorageAdmin(ctx context.Context, directory, node, workerRuntime string, input map[string]any, lock *os.File, jsonOutput bool, output io.Writer) error {
	var data json.RawMessage
	progress := output
	if jsonOutput {
		progress = io.Discard
	}
	err := installationPhase(ctx, progress, "Preparing storage administration; cleanup verifies retained bytes", 0, func(_ *installPhase) error {
		var e error
		data, e = hoststorage.Call(ctx, node, workerRuntime, directory, input, lock)
		return e
	})
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(output).Encode(data)
	}
	var value struct {
		Kind                string `json:"kind"`
		PlanID              string `json:"planId"`
		BatchID             string `json:"batchId"`
		Action              string `json:"action"`
		State               string `json:"state"`
		Captures            int    `json:"captures"`
		LogicalBytes        int64  `json:"logicalBytes"`
		Note                string `json:"note"`
		RetainedBudgetBytes int64  `json:"retainedBudgetBytes"`
		MinFreeBytes        int64  `json:"minFreeBytes"`
	}
	if err = json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Kind == "checkpoint-storage-policy" {
		_, err = fmt.Fprintf(output, "Retained storage budget: %s; free-volume warning: %s\nBudgets warn only; zero disables that threshold. No work is stopped and no data is removed.\n", checkpointSize(value.RetainedBudgetBytes), checkpointSize(value.MinFreeBytes))
		return err
	}
	if value.Kind != "checkpoint-cleanup-result" {
		return errors.New("The storage helper returned an incompatible administration result")
	}
	fmt.Fprintf(output, "Cleanup %s: %s\nPlan ID: %s\nQuarantine batch: %s\nPlanned captures: %d (%s logical file entries)\n%s\n", value.Action, value.State, value.PlanID, value.BatchID, value.Captures, checkpointSize(value.LogicalBytes), value.Note)
	if value.State == "planned" {
		fmt.Fprintf(output, "Apply this exact plan with cleanup apply --plan %s and the same --data-dir.\n", value.PlanID)
		if value.Action == "purge" {
			fmt.Fprintln(output, "Permanent purge additionally requires --confirm-permanent.")
		}
	}
	return nil
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
		Schema    int    `json:"schema"`
		Kind      string `json:"kind"`
		Complete  bool   `json:"complete"`
		Quiescent bool   `json:"quiescent"`
		Budget    struct {
			Observation struct {
				Level               string   `json:"level"`
				Warnings            []string `json:"warnings"`
				LogicalBytes        int64    `json:"logicalBytes"`
				Complete            bool     `json:"complete"`
				FreeBytes           *int64   `json:"freeBytes"`
				RetainedBudgetBytes int64    `json:"retainedBudgetBytes"`
				MinFreeBytes        int64    `json:"minFreeBytes"`
			} `json:"observation"`
		} `json:"budget"`
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
		if !report.Quiescent {
			fmt.Fprintln(output, "Live observation; stop the owning Runner before creating a cleanup plan.")
		}
		if report.Budget.Observation.Level != "" {
			b := report.Budget.Observation
			fmt.Fprintf(output, "Storage budget: %s; retained limit %s, free-volume warning %s\n", b.Level, checkpointSize(b.RetainedBudgetBytes), checkpointSize(b.MinFreeBytes))
			if b.FreeBytes != nil {
				fmt.Fprintf(output, "Observed free volume space: %s\n", checkpointSize(*b.FreeBytes))
			}
			for _, warning := range b.Warnings {
				fmt.Fprintf(output, "  Budget observation: %s\n", warning)
			}
		}
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
