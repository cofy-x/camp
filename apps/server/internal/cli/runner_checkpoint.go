package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
)

func runRunnerCheckpoint(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		_, err := fmt.Fprintln(output, `Usage: camp runner checkpoint status|storage|observe|restore|reconstruct|observe-reconstruction --data-dir PATH [options]

All commands require an explicit private Runner directory. Status reads retained
metadata without starting a worker. Stop the Runner before the other operations.
Storage reports local disk use and a conservative retention preview, offline.
It never removes data, starts a worker or measures Linux Store/VM storage.
Observe reconciles outcomes without replaying tools, mutations or model requests.

  --json            Machine-readable storage report, including incomplete observations
  --operation ID    Retained checkpoint operation
  --parent PATH     Existing owner-only directory; restore creates a fresh child
  --from-native     Restore retained native bytes explicitly, without claiming a current worker verification
  --recovery ID     Interrupted reconstruction intent to observe and select after verification

Reconstruct creates a new owned Store and operation from the retained native
snapshot, then selects its verified epoch. It preserves old captures, uncertain
receipts, Runner identity and journal. No action publishes to Camp Server.`)
		return err
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return errors.New("Checkpoint administration requires an ARM64 Mac")
	}
	action := args[0]
	if action != "status" && action != "storage" && action != "observe" && action != "restore" && action != "reconstruct" && action != "observe-reconstruction" {
		return errors.New("Unknown checkpoint command")
	}
	flags := flag.NewFlagSet("runner checkpoint "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var directory, operation, parent, recovery string
	var fromNative, jsonOutput bool
	flags.StringVar(&directory, "data-dir", "", "explicit private Runner directory")
	flags.StringVar(&operation, "operation", "", "retained operation ID")
	flags.StringVar(&parent, "parent", "", "private restoration parent")
	flags.StringVar(&recovery, "recovery", "", "retained reconstruction intent ID")
	flags.BoolVar(&fromNative, "from-native", false, "explicitly restore retained native bytes")
	flags.BoolVar(&jsonOutput, "json", false, "machine-readable storage report")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if directory == "" || flags.NArg() != 0 {
		return errors.New("Checkpoint administration requires --data-dir and no positional arguments")
	}
	if jsonOutput && action != "storage" || action == "storage" && (operation != "" || parent != "" || recovery != "" || fromNative) {
		return errors.New("Storage accepts only --data-dir and --json; --json applies only to storage")
	}
	if action == "restore" && parent == "" || fromNative && action != "restore" {
		return errors.New("Choose --parent for restore; --from-native applies only to restoration")
	}
	if action != "status" && action != "storage" && action != "observe-reconstruction" && operation == "" {
		return errors.New("Choose a retained --operation")
	}
	if action == "observe-reconstruction" && recovery == "" {
		return errors.New("Choose a retained --recovery intent")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	var lock *os.File
	if action != "status" {
		lock, err = execution.AcquireLock(directory)
		if err != nil {
			return fmt.Errorf("Stop this Runner and its native jobs before checkpoint administration: %w", err)
		}
		if lock != nil {
			defer lock.Close()
		}
	}
	config, err := daemon.LoadConfig(directory)
	if err != nil {
		return err
	}
	if action == "status" {
		if _, err := os.Lstat(filepath.Join(directory, "layerfs-worker")); errors.Is(err, os.ErrNotExist) {
			return json.NewEncoder(output).Encode(map[string]any{"configured": false, "workspaceBackend": config.WorkspaceBackend, "workerObserved": false})
		}
	}
	bundle := runnerBundleDirectory()
	if bundle == "" {
		bundle = config.BundleDir
	}
	if hasRuntimePin() {
		pin, err := compiledRuntimePin()
		if err != nil {
			return err
		}
		bundle = pin.cache(directory)
		verify := func(progress *installPhase) error {
			_, e := verifyPinnedRuntimeContext(ctx, bundle, pin, progress)
			return e
		}
		if action == "storage" && !jsonOutput {
			err = installationPhase(ctx, output, "Checking fixed runtime files", 0, verify)
		} else {
			err = verify(nil)
		}
		if err != nil {
			return err
		}
	}
	native := runnerNative(bundle, config.DockerSocket)
	node, err := exec.LookPath(native.NodePath)
	if err != nil {
		return err
	}
	workerRuntime, err := filepath.Abs(native.WorkerRuntimeDir)
	if err != nil {
		return err
	}
	if action == "storage" {
		return runCheckpointStorage(ctx, directory, node, workerRuntime, config.RunnerID, config.StorageID, lock, jsonOutput, output)
	}
	if _, err := os.Stat(filepath.Join(workerRuntime, "manage.mjs")); err != nil {
		return errors.New("Install the Camp checkpoint worker administration runtime before using this command")
	}
	docker, dockerErr := nativeexecutor.SelectedDockerClient(config.DockerBinary)
	if dockerErr != nil && action != "status" && !fromNative {
		return dockerErr
	}
	parameters := map[string]any{"action": action, "dataRoot": directory, "home": filepath.Join(directory, "layerfs-worker"), "runnerId": config.RunnerID, "storageId": config.StorageID, "docker": config.DockerSocket, "dockerBinary": docker, "installation": filepath.Join(workerRuntime, "installed.json"), "operationId": operation, "parent": parent, "fromNative": fromNative, "recoveryId": recovery}
	input, err := json.Marshal(parameters)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, node, filepath.Join(workerRuntime, "manage.mjs"))
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + directory, "LANG=en_US.UTF-8"}
	if lock != nil {
		cmd.ExtraFiles = []*os.File{lock}
	}
	cmd.Stdin, cmd.Stdout = bytes.NewReader(input), output
	var diagnostic bytes.Buffer
	cmd.Stderr = &checkpointDiagnostic{writer: &diagnostic}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Checkpoint administration failed; retained data was not reset: %w %s", err, diagnostic.String())
	}
	return nil
}

type checkpointDiagnostic struct{ writer *bytes.Buffer }

func (w *checkpointDiagnostic) Write(data []byte) (int, error) {
	length := len(data)
	if available := 8192 - w.writer.Len(); available > 0 {
		if len(data) > available {
			data = data[:available]
		}
		_, _ = w.writer.Write(data)
	}
	return length, nil
}
