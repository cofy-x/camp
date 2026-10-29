package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

type sourceFlags map[string]string

func (s *sourceFlags) String() string { return "alias=/absolute/directory" }
func (s *sourceFlags) Set(value string) error {
	alias, directory, ok := strings.Cut(value, "=")
	if !ok || !p.ValidSourceID(alias) || !filepath.IsAbs(directory) {
		return errors.New("Sources require alias=/absolute/directory; aliases start with a lowercase letter and use at most 48 lowercase letters, digits, hyphens or underscores")
	}
	if *s == nil {
		*s = sourceFlags{}
	}
	if _, exists := (*s)[alias]; !exists && len(*s) >= p.MaxSources {
		return fmt.Errorf("A Runner supports at most %d source aliases", p.MaxSources)
	}
	(*s)[alias] = filepath.Clean(directory)
	return nil
}

func runRunner(ctx context.Context, args []string, output io.Writer) error {
	return runRunnerWithInput(ctx, args, output, runnerInput{reader: os.Stdin, isTerminal: runnerTerminal, readSecret: readConnectionCode})
}

func runRunnerWithInput(ctx context.Context, args []string, output io.Writer, input runnerInput) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" || (len(args) == 2 && (args[1] == "--help" || args[1] == "-h")) {
		_, err := fmt.Fprint(output, `Usage: camp runner <setup|run|status|service> [options]

  setup             Guided setup in a terminal
  setup --server https://camp.example --name linux-a --data-dir /private/camp-runner --code-file /private/connect-code
  run --data-dir /private/camp-runner
  status --data-dir /private/camp-runner
  service <install|start|stop|status|enable-boot|disable-boot|remove> --data-dir /private/camp-runner

Setup connects to Camp, installs its fixed runtime once, loads its execution image,
and checks isolated execution without a model request. The offline package includes
the same runtime. Existing verified runtime downloads are reused.
Model settings stay on Camp Server. No separate DSH or LayerFS installation is needed.
Scripts provide --server, --name, --data-dir and --code-file explicitly.

Options:
  --data-dir path    Private Runner storage (default: user config directory/camp/runner)
  --server URL       Camp HTTPS origin (HTTP only on localhost)
  --name name        Runner display name
  --code-file path   Private connection code file; - reads stdin (default)
  --ca-file path     Additional CA trust bundle; TLS verification remains enabled
  --slots count      Concurrent local operations (default 1)
  --source alias=path Register an authorized snapshot source (repeatable)
  --docker-socket path Local Docker socket (default /var/run/docker.sock)

Setup never installs a system service. Service installation, starting it, and
running it at boot are separate explicit actions. Removing a service keeps data.
`)
		return err
	}
	if args[0] == "service" {
		return runRunnerService(ctx, args[1:], output)
	}
	action := args[0]
	if action != "setup" && action != "run" && action != "status" {
		return fmt.Errorf("Unknown Runner command %q", action)
	}
	flags := flag.NewFlagSet("runner "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var directory, server, name, codeFile, caFile, executor, bundle, docker string
	var slots int
	var sources sourceFlags
	var development bool
	flags.StringVar(&directory, "data-dir", "", "private Runner directory")
	if action == "setup" {
		flags.StringVar(&server, "server", "", "Camp origin")
		flags.StringVar(&name, "name", "", "Runner name")
		flags.StringVar(&codeFile, "code-file", "-", "connection code file or stdin")
		flags.StringVar(&caFile, "ca-file", "", "additional CA trust bundle")
		flags.IntVar(&slots, "slots", 1, "concurrent operations")
		flags.Var(&sources, "source", "authorized source alias=path")
		flags.StringVar(&executor, "executor", "native", "native or explicit fixture protocol tests")
		flags.StringVar(&bundle, "bundle-dir", runnerBundleDirectory(), "Camp installation directory")
		flags.BoolVar(&development, "development-runtime", false, "use an explicitly configured source checkout runtime")
	}
	if action != "status" {
		flags.StringVar(&docker, "docker-socket", daemonDefault("CAMP_DOCKER_SOCKET", "/var/run/docker.sock"), "local Docker socket")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("Runner commands do not accept positional arguments")
	}
	explicitCode, explicitDocker, explicitCA := false, false, false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "code-file":
			explicitCode = true
		case "docker-socket":
			explicitDocker = true
		case "ca-file":
			explicitCA = true
		}
	})
	interactive := input.isTerminal(input.reader)
	if action == "setup" && !interactive && (directory == "" || server == "" || name == "") {
		return errors.New("Noninteractive setup requires --server, --name and --data-dir; provide --code-file for a new connection")
	}
	if action == "setup" && interactive {
		location, err := promptRunnerSetup(input.reader, output, runnerSetupLocation{directory: directory, server: server, name: name})
		if err != nil {
			return err
		}
		directory, server, name = location.directory, location.server, location.name
	}
	if directory == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		directory = filepath.Join(base, "camp", "runner")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	if action == "status" {
		return daemon.Status(directory, output)
	}
	if action == "run" {
		config, err := daemon.LoadConfig(directory)
		if err != nil {
			return err
		}
		bundle = runnerBundleDirectory()
		if bundle == "" {
			bundle = config.BundleDir
		}
		if config.Executor == "native" && runtimePinJSON != "" {
			pin, err := compiledRuntimePin()
			if err != nil {
				return err
			}
			bundle = pin.cache(directory)
			if _, err := verifyPinnedRuntime(bundle, pin); err != nil {
				config.NativeUnavailable = "The fixed runtime is missing or failed verification. Stop this Runner and repeat setup with the same data directory: " + err.Error()
			}
		} else if bundle != "" && config.Executor == "native" {
			if _, err := verifyRunnerBundle(bundle); err != nil {
				config.NativeUnavailable = err.Error()
			}
		}
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "docker-socket" {
				config.DockerSocket = docker
			}
		})
		if config.DockerSocket == "" {
			config.DockerSocket = docker
		}
		config.Native = runnerNative(bundle, config.DockerSocket)
		return daemon.Run(ctx, config, output)
	}
	previous, loadErr := daemon.LoadConfig(directory)
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		return loadErr
	}
	if loadErr == nil {
		if !explicitDocker && previous.DockerSocket != "" {
			docker = previous.DockerSocket
		}
		if !explicitCA {
			caFile = previous.CAFile
		}
	}
	needsCode := loadErr != nil || (previous.PendingToken == "" && previous.RunnerID == "")
	if needsCode && !interactive && !explicitCode {
		return errors.New("Noninteractive setup requires --code-file; use --code-file - to read the connection code from stdin")
	}
	if executor != "native" && executor != "fixture" {
		return errors.New("Executor must be native or fixture")
	}
	if !filepath.IsAbs(docker) {
		return errors.New("Choose an absolute local Docker socket")
	}
	var installationErr error
	var managedPin *runtimePin
	var manifest bundleManifest
	launcherBundle := bundle
	if executor == "native" && !development {
		if bundle == "" || runtimePinJSON != "" {
			pin, err := compiledRuntimePin()
			if err != nil {
				return err
			}
			managedPin = &pin
			bundle, launcherBundle = "", ""
		} else {
			bundle, err = filepath.Abs(bundle)
			if err != nil {
				return err
			}
			fmt.Fprintln(output, "Checking Camp Runner installation…")
			manifest, err = verifyRunnerBundle(bundle)
			if err != nil {
				return err
			}
		}
	}
	if caFile != "" {
		caFile, err = filepath.Abs(caFile)
		if err != nil {
			return err
		}
	}
	var data []byte
	if needsCode {
		var codeInput io.Reader = input.reader
		if codeFile != "-" {
			file, err := os.Open(codeFile)
			if err != nil {
				return err
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
				return errors.New("Connection code files must be regular private files (mode 600)")
			}
			codeInput = file
		}
		if codeFile == "-" {
			if interactive {
				fmt.Fprintln(output, "Enter the connection code (input is hidden):")
			}
			data, err = input.readSecret(codeInput)
			if interactive {
				fmt.Fprintln(output)
			}
		} else {
			data, err = io.ReadAll(io.LimitReader(codeInput, 8193))
		}
		if err != nil {
			return err
		}
		if len(data) > 8192 {
			return errors.New("The connection code is too large")
		}
	}
	config, err := daemon.Join(ctx, daemon.Config{DataDir: directory, ServerURL: strings.TrimRight(server, "/"), Name: name, CAFile: caFile, CAFileSet: explicitCA, Executor: executor, Slots: slots, Sources: sources, BundleDir: bundle, DockerSocket: docker}, string(data))
	if err != nil {
		return err
	}
	fmt.Fprintln(output, "Connected account:", config.Name, "— registration saved.")
	config.DockerSocket = docker
	if err = daemon.SaveInstallation(config, config.BundleDir, docker); err != nil {
		return err
	}
	if managedPin != nil {
		bundle, err = installRunnerRuntime(ctx, config, *managedPin, output)
		if err == nil {
			manifest, err = verifyPinnedRuntime(bundle, *managedPin)
		}
		if err != nil {
			return err
		}
	}
	if executor == "native" && !development {
		fmt.Fprintln(output, "Preparing the fixed execution image…")
		installationErr = loadRunnerImage(ctx, bundle, docker, manifest.LayerFS.Image)
	}
	if err = daemon.SaveInstallation(config, bundle, docker); err != nil {
		return err
	}
	if executor == "native" {
		report, err := saveRunnerReadiness(ctx, directory, runnerNative(bundle, docker), installationErr)
		if err != nil {
			return err
		}
		if err = json.NewEncoder(output).Encode(report); err != nil {
			return err
		}
		if !report.Ready {
			return errors.New("Runner registration is saved, but execution needs attention. Correct the reported issue and repeat setup")
		}
	}
	return runnerNextSteps(output, launcherBundle, directory)
}

func runnerNative(bundle, docker string) nativeexecutor.Config {
	config := nativeexecutor.Config{RuntimeDir: daemonDefault("CAMP_DSH_RUNTIME", "tools/dsh-runtime"), LayerFSRuntimeDir: daemonDefault("CAMP_LAYERFS_RUNTIME", "tools/layerfs-runtime"), SandboxHelper: daemonDefault("CAMP_SANDBOX_HELPER", "dist/camp-sandbox"), NodePath: daemonDefault("CAMP_DSH_NODE", "node"), DockerSocket: docker}
	if bundle != "" {
		config.RuntimeDir = filepath.Join(bundle, "runtime/dsh-runtime")
		config.LayerFSRuntimeDir = filepath.Join(bundle, "runtime/layerfs-runtime")
		config.SandboxHelper = filepath.Join(bundle, "bin/camp-sandbox")
		config.NodePath = filepath.Join(bundle, "bin/node")
	}
	return config
}

// Setup owns the journal lock through publishing its observations. It must not
// overwrite a running daemon's readiness or clean any of its native state.
func saveRunnerReadiness(ctx context.Context, directory string, config nativeexecutor.Config, installationErr error) (p.ReadinessReport, error) {
	lock, err := execution.AcquireLock(directory)
	if err != nil {
		return p.ReadinessReport{}, err
	}
	if lock != nil {
		defer lock.Close()
	}
	report := nativeexecutor.HostChecks(config)
	if installationErr != nil {
		report.Checks = append(report.Checks, p.ReadinessCheck{ID: "image", Label: "Bundled execution image", Status: "failed", Detail: installationErr.Error()})
	} else {
		config.Lock = lock
		report = probeRunner(ctx, directory, config)
	}
	return report, daemon.WriteReadiness(directory, report)
}

func probeRunner(ctx context.Context, directory string, config nativeexecutor.Config) p.ReadinessReport {
	report := nativeexecutor.HostChecks(config)
	scratch, err := os.MkdirTemp(directory, ".setup-check-")
	if err == nil {
		defer os.RemoveAll(scratch)
		config.DataDir = scratch
		probeCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
		defer cancel()
		var executor *nativeexecutor.Executor
		executor, err = nativeexecutor.Open(probeCtx, config)
		if err == nil {
			report = executor.SelfTest(probeCtx)
			if closeErr := executor.Close(); closeErr != nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		report.Ready = false
		report.Checks = append(report.Checks, p.ReadinessCheck{ID: "runtime", Label: "Bundled execution runtime", Status: "failed", Detail: err.Error()})
	}
	return report
}

func daemonDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
