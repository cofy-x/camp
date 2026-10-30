package cli

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
)

type runnerService struct {
	directory string
	command   func(context.Context, ...string) error
}

func serviceName(directory string) string {
	digest := sha256.Sum256([]byte(directory))
	return fmt.Sprintf("camp-runner-%x.service", digest[:8])
}
func validateServiceOwner(executor string, owner uint32) error {
	if executor == "native" && owner != 0 {
		return errors.New("Native Runner services require root-owned private storage. Use the same dedicated privileged account for setup, foreground execution and service installation; use a new private directory when changing accounts. Existing data was not changed")
	}
	return nil
}
func systemdArgument(value string) (string, error) {
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("Service paths cannot contain line breaks or NUL")
	}
	// systemd specifier and environment expansion are distinct from shell quoting.
	return strconv.Quote(strings.ReplaceAll(strings.ReplaceAll(value, "%", "%%"), "$", "$$")), nil
}
func serviceUnit(executable, directory string) (string, error) {
	if !filepath.IsAbs(executable) || !filepath.IsAbs(directory) {
		return "", errors.New("Service paths must be absolute")
	}
	binary, err := systemdArgument(executable)
	if err != nil {
		return "", err
	}
	data, err := systemdArgument(directory)
	if err != nil {
		return "", err
	}
	return "# Managed by Camp Runner\n[Unit]\nDescription=Camp Runner\nAfter=network-online.target docker.service\nWants=network-online.target\n\n[Service]\nType=simple\nUMask=0077\nExecStart=" + binary + " runner run --data-dir " + data + fmt.Sprintf("\nRestart=on-failure\nRestartPreventExitStatus=%d\n", ExitNeedsAttention) + "RestartSec=5\nKillMode=control-group\nTimeoutStopSec=180\n\n[Install]\nWantedBy=multi-user.target\n", nil
}
func (s runnerService) apply(ctx context.Context, action, binary, directory string) error {
	name := serviceName(directory)
	path := filepath.Join(s.directory, name)
	switch action {
	case "install":
		unit, err := serviceUnit(binary, directory)
		if err != nil {
			return err
		}
		if old, readErr := os.ReadFile(path); readErr == nil && !strings.HasPrefix(string(old), "# Managed by Camp Runner\n") {
			return errors.New("Refusing to replace an unmanaged service")
		} else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		if err = os.MkdirAll(s.directory, 0755); err != nil {
			return err
		}
		temporary, err := os.CreateTemp(s.directory, ".camp-service-")
		if err != nil {
			return err
		}
		defer os.Remove(temporary.Name())
		_, err = temporary.WriteString(unit)
		if err == nil {
			err = temporary.Chmod(0644)
		}
		err = errors.Join(err, temporary.Sync(), temporary.Close())
		if err != nil {
			return err
		}
		if err = os.Rename(temporary.Name(), path); err != nil {
			return err
		}
		return s.command(ctx, "daemon-reload")
	case "remove":
		existing, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(existing), "# Managed by Camp Runner\n") {
			return errors.New("Refusing to remove an unmanaged service")
		}
		if err = s.command(ctx, "stop", name); err != nil {
			return err
		}
		if err = s.command(ctx, "disable", name); err != nil {
			return err
		}
		if err = os.Remove(path); err != nil {
			return err
		}
		return s.command(ctx, "daemon-reload")
	case "start", "stop", "status":
		return s.command(ctx, action, name)
	case "enable-boot":
		return s.command(ctx, "enable", name)
	case "disable-boot":
		return s.command(ctx, "disable", name)
	default:
		return fmt.Errorf("Unknown service action %q", action)
	}
}
func runRunnerService(ctx context.Context, args []string, output io.Writer) error {
	if runtime.GOOS == "darwin" {
		return runMacRunnerService(ctx, args, output)
	}
	if len(args) == 0 {
		return errors.New("Choose service install, start, stop, status, enable-boot, disable-boot or remove")
	}
	action := args[0]
	switch action {
	case "install", "start", "stop", "status", "enable-boot", "disable-boot", "remove":
	default:
		return fmt.Errorf("Unknown service action %q", action)
	}
	flags := flag.NewFlagSet("runner service", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var directory string
	flags.StringVar(&directory, "data-dir", "", "private Runner storage")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("Service commands do not accept positional arguments")
	}
	if directory == "" {
		return errors.New("Service commands require an explicit --data-dir")
	}
	if runtime.GOOS != "linux" {
		return errors.New("Runner services require Linux systemd")
	}
	if action != "status" && os.Geteuid() != 0 {
		return errors.New("System service changes require the machine administrator; no elevation was attempted")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	config, err := daemon.LoadConfig(directory)
	if err != nil {
		return err
	}
	bundle := runnerBundleDirectory()
	if bundle == "" {
		bundle = config.BundleDir
	}
	if action == "install" && bundle == "" {
		return errors.New("Install the service from the complete Camp Runner bundle")
	}
	binary := filepath.Join(bundle, "camp")
	if hasRuntimePin() {
		pin, err := compiledRuntimePin()
		if err != nil {
			return err
		}
		if action == "install" && config.BundleDir != pin.cache(directory) {
			return errors.New("Complete setup with this installer before installing its service")
		}
		binary, err = os.Executable()
		if err != nil {
			return err
		}
	}
	if action == "install" {
		owner, err := serviceDataOwner(directory)
		if err != nil {
			return err
		}
		if err := validateServiceOwner(config.Executor, owner); err != nil {
			return err
		}
		info, err := os.Stat(binary)
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0111 == 0 {
			return errors.New("The Camp Runner launcher is not executable")
		}
	}
	manager := runnerService{directory: "/etc/systemd/system", command: func(ctx context.Context, args ...string) error {
		cmd := exec.CommandContext(ctx, "systemctl", args...)
		cmd.Stdout = output
		cmd.Stderr = output
		return cmd.Run()
	}}
	if err = manager.apply(ctx, action, binary, directory); err != nil {
		return err
	}
	fmt.Fprintln(output, "Runner service", action, "completed. Runner data was preserved.")
	if action == "install" {
		fmt.Fprintln(output, "The service is installed but not started or enabled at boot. Use service start and service enable-boot explicitly.")
	}
	return nil
}
