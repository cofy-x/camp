package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
	"github.com/cofy-x/camp/apps/server/internal/execution"
)

func runMacRunnerService(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("Choose service install, start, stop, status, enable-login, disable-login or remove")
	}
	action := args[0]
	switch action {
	case "install", "start", "stop", "status", "enable-login", "disable-login", "remove":
	default:
		return fmt.Errorf("Unknown Mac service action %q", action)
	}
	if os.Geteuid() == 0 {
		return errors.New("Run the Mac Runner service from its original unprivileged account, without sudo")
	}
	flags := flag.NewFlagSet("runner service", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var directory string
	flags.StringVar(&directory, "data-dir", "", "private Runner storage")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || directory == "" {
		return errors.New("Service commands require an explicit --data-dir and no positional arguments")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	owner, err := serviceDataOwner(directory)
	if err != nil {
		return err
	}
	if owner != uint32(os.Geteuid()) {
		return errors.New("This private Runner directory belongs to another account; ownership and identity were preserved")
	}
	config, err := daemon.LoadConfig(directory)
	if err != nil {
		return err
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	if action == "install" {
		lock, err := execution.AcquireLock(directory)
		if err != nil {
			return err
		}
		defer lock.Close()
		if hasRuntimePin() {
			pin, err := compiledRuntimePin()
			if err != nil {
				return err
			}
			if err := verifyServiceRuntime(directory, config.BundleDir, pin); err != nil {
				return err
			}
		} else {
			bundle := runnerBundleDirectory()
			if bundle == "" {
				bundle = config.BundleDir
			}
			if bundle == "" {
				return errors.New("Install the background service from a complete Camp Runner package; source runtimes support foreground execution")
			}
			binary = filepath.Join(bundle, "camp")
		}
		info, err := os.Stat(binary)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return errors.New("The Camp Runner launcher is not executable")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	run := func(ctx context.Context, args ...string) error {
		cmd := exec.CommandContext(ctx, "/bin/launchctl", args...)
		cmd.Stdout, cmd.Stderr = output, output
		return cmd.Run()
	}

	domain := macServiceDomain(ctx)
	manager := macService{domain: domain, otherDomains: []string{"gui/" + strconv.Itoa(os.Geteuid()), "user/" + strconv.Itoa(os.Geteuid())}, directory: directory, loginDirectory: filepath.Join(home, "Library", "LaunchAgents"), command: run, lookup: lookupMacService}
	if err := manager.apply(ctx, action, binary); err != nil {
		return err
	}
	fmt.Fprintln(output, "Runner service", action, "completed. Runner data was preserved.")
	if action == "install" {
		fmt.Fprintln(output, "The service is installed without starting it or enabling login startup. Choose service start and service enable-login explicitly.")
	}
	return nil
}
