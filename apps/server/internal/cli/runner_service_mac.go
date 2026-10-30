package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
	"github.com/cofy-x/camp/apps/server/internal/execution"
)

const macServiceHeader = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!-- Managed by Camp Runner -->\n"

type macRunnerService struct {
	domain, directory, loginDirectory string
	otherDomains                      []string
	command                           func(context.Context, ...string) error
	loaded                            func(context.Context, string) bool
}

func (s macRunnerService) loadedTargets(ctx context.Context) []string {
	var targets []string
	seen := map[string]bool{}
	for _, domain := range append([]string{s.domain}, s.otherDomains...) {
		if seen[domain] {
			continue
		}
		seen[domain] = true
		target := domain + "/" + macServiceLabel(s.directory)
		if s.loaded(ctx, target) {
			targets = append(targets, target)
		}
	}
	return targets
}

func macServiceLabel(directory string) string {
	digest := sha256.Sum256([]byte(directory))
	return fmt.Sprintf("dev.cofy.camp.runner.%x", digest[:8])
}

func macServiceDomain(ctx context.Context) string {
	domain := "gui/" + strconv.Itoa(os.Geteuid())
	if exec.CommandContext(ctx, "/bin/launchctl", "print", domain).Run() != nil {
		domain = "user/" + strconv.Itoa(os.Geteuid())
	}
	return domain
}

func macServicePlist(binary, directory string) ([]byte, error) {
	for _, value := range []string{binary, directory} {
		if !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
			return nil, errors.New("Service paths must be absolute and contain no line breaks or NUL")
		}
	}
	var b bytes.Buffer
	b.WriteString(macServiceHeader + "<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict>\n")
	text := func(value string) { _ = xml.EscapeText(&b, []byte(value)) }
	entry := func(key, value string) {
		b.WriteString("<key>" + key + "</key><string>")
		text(value)
		b.WriteString("</string>\n")
	}
	entry("Label", macServiceLabel(directory))
	b.WriteString("<key>ProgramArguments</key><array>")
	for _, arg := range []string{binary, "runner", "run", "--data-dir", directory} {
		b.WriteString("<string>")
		text(arg)
		b.WriteString("</string>")
	}
	b.WriteString("</array>\n")
	entry("WorkingDirectory", directory)
	entry("StandardOutPath", filepath.Join(directory, "logs", "runner.log"))
	entry("StandardErrorPath", filepath.Join(directory, "logs", "runner-error.log"))
	b.WriteString("<key>EnvironmentVariables</key><dict><key>PATH</key><string>/usr/bin:/bin:/usr/sbin:/sbin</string></dict>\n")
	// The process reconnects itself. A failure requiring operator attention must
	// remain stopped rather than launchd replaying initialization indefinitely.
	b.WriteString("<key>RunAtLoad</key><true/><key>KeepAlive</key><false/><key>Umask</key><integer>63</integer><key>ExitTimeOut</key><integer>180</integer>\n</dict></plist>\n")
	return b.Bytes(), nil
}

func readManagedMacPlist(name string) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("Refusing an unmanaged or linked service file")
	}
	data, err := os.ReadFile(name)
	if err == nil && !bytes.HasPrefix(data, []byte(macServiceHeader)) {
		err = errors.New("Refusing to replace or remove an unmanaged service")
	}
	return data, err
}

func writeMacPlist(name string, data []byte) error {
	if _, err := readManagedMacPlist(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".camp-service-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}

func (s macRunnerService) apply(ctx context.Context, action, binary string) error {
	label := macServiceLabel(s.directory)
	target := s.domain + "/" + label
	private := filepath.Join(s.directory, "service", label+".plist")
	login := filepath.Join(s.loginDirectory, label+".plist")
	loaded := s.loadedTargets(ctx)
	if action == "install" {
		if len(loaded) > 0 {
			return errors.New("Stop this Runner service before updating its installation")
		}
		data, err := macServicePlist(binary, s.directory)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(s.directory, "logs"), 0700); err != nil {
			return err
		}
		if err := writeMacPlist(private, data); err != nil {
			return err
		}
		if _, err := readManagedMacPlist(login); err == nil {
			return writeMacPlist(login, data)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := readManagedMacPlist(private)
	if err != nil {
		return fmt.Errorf("Install this Runner service first: %w", err)
	}
	switch action {
	case "start":
		if len(loaded) > 1 {
			return errors.New("This Runner is registered in multiple login domains; stop its service before starting it again")
		}
		if len(loaded) == 1 {
			return s.command(ctx, "kickstart", loaded[0])
		}
		return s.command(ctx, "bootstrap", s.domain, private)
	case "status":
		if len(loaded) == 1 {
			target = loaded[0]
		}
		return s.command(ctx, "print", target)
	case "stop", "remove":
		for _, current := range loaded {
			if err := s.command(ctx, "bootout", current); err != nil {
				return err
			}
		}
		if action == "stop" {
			return nil
		}
		if _, err := readManagedMacPlist(login); err == nil {
			if err := os.Remove(login); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return os.Remove(private)
	case "enable-login":
		return writeMacPlist(login, data)
	case "disable-login":
		if _, err := readManagedMacPlist(login); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		return os.Remove(login)
	default:
		return fmt.Errorf("Unknown Mac service action %q", action)
	}
}

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
	loaded := func(ctx context.Context, target string) bool {
		return exec.CommandContext(ctx, "/bin/launchctl", "print", target).Run() == nil
	}
	domain := macServiceDomain(ctx)
	manager := macRunnerService{domain: domain, otherDomains: []string{"gui/" + strconv.Itoa(os.Geteuid()), "user/" + strconv.Itoa(os.Geteuid())}, directory: directory, loginDirectory: filepath.Join(home, "Library", "LaunchAgents"), command: run, loaded: loaded}
	if err := manager.apply(ctx, action, binary); err != nil {
		return err
	}
	fmt.Fprintln(output, "Runner service", action, "completed. Runner data was preserved.")
	if action == "install" {
		fmt.Fprintln(output, "The service is installed without starting it or enabling login startup. Choose service start and service enable-login explicitly.")
	}
	return nil
}
