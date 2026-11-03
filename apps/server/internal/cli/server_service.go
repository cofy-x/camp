package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/servicelog"
	"github.com/cofy-x/camp/apps/server/internal/serviceowner"
)

type serverServiceConfig struct {
	Schema      int               `json:"schema"`
	DataDir     string            `json:"dataDir"`
	Arguments   []string          `json:"arguments"`
	Environment map[string]string `json:"environment"`
}

var serverServiceEnvironment = map[string]bool{"CAMP_PUBLIC_ORIGIN": true, "CAMP_IMPORT_ROOTS": true, "CAMP_DOCUMENT_NODE": true, "CAMP_DOCUMENT_RUNTIME": true}

func serverServiceSettings(options serveOptions, getenv func(string) string, cwd string) (serverServiceConfig, error) {
	c, err := resolveConfig(options, getenv, os.UserConfigDir)
	if err != nil {
		return serverServiceConfig{}, err
	}
	directory, err := filepath.Abs(c.DataDir)
	if err != nil {
		return serverServiceConfig{}, err
	}
	config := serverServiceConfig{Schema: 1, DataDir: directory, Environment: map[string]string{}}
	config.Arguments = []string{"--addr", c.Addr, "--data-dir", directory}
	for _, v := range []struct {
		flag, value string
		path        bool
	}{
		{"--tls-cert", c.TLSCert, true}, {"--tls-key", c.TLSKey, true},
		{"--allowed-hosts", strings.Join(c.AllowedHosts, ","), false}, {"--trusted-origins", strings.Join(c.TrustedOrigins, ","), false},
	} {
		if v.value != "" {
			value := v.value
			if v.path && !filepath.IsAbs(value) {
				value = filepath.Join(cwd, value)
			}
			config.Arguments = append(config.Arguments, v.flag, value)
		}
	}
	if c.MaxConcurrentRuns != 0 {
		config.Arguments = append(config.Arguments, "--max-concurrent-runs", strconv.Itoa(c.MaxConcurrentRuns))
	}
	if c.AllowFixtureRunners {
		config.Arguments = append(config.Arguments, "--allow-fixture-runners")
	}
	file := options.envFile
	if file == "" {
		file = getenv("CAMP_ENV_FILE")
	}
	explicit := file != ""
	if file == "" {
		file = filepath.Join(cwd, ".env")
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(cwd, file)
	}
	if _, err := os.Stat(file); err == nil || explicit {
		config.Arguments = append(config.Arguments, "--env-file", file)
	} else if !errors.Is(err, os.ErrNotExist) {
		return config, err
	}
	for name := range serverServiceEnvironment {
		if value := getenv(name); value != "" {
			if (name == "CAMP_DOCUMENT_NODE" || name == "CAMP_DOCUMENT_RUNTIME") && !filepath.IsAbs(value) {
				value = filepath.Join(cwd, value)
			}
			config.Environment[name] = value
		}
	}
	if err := validateServerServiceConfig(config, directory); err != nil {
		return config, err
	}
	return config, nil
}

func readServerServiceConfig(directory string) (serverServiceConfig, error) {
	var config serverServiceConfig
	name := filepath.Join(directory, "service", "server.json")
	info, err := os.Lstat(name)
	if err != nil {
		return config, err
	}
	owner, err := serviceDataOwner(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 64<<10 || owner != uint32(os.Geteuid()) {
		return config, errors.New("Server service configuration must be a private owned regular file")
	}
	file, err := os.Open(name)
	if err != nil {
		return config, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, errors.New("Invalid Server service configuration")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return config, errors.New("Invalid Server service configuration")
	}
	if err := validateServerServiceConfig(config, directory); err != nil {
		return config, err
	}
	return config, nil
}

// Install and read share one bounded configuration contract. No environment
// file, model credential, service or filesystem owner is opened here.
func validateServerServiceConfig(config serverServiceConfig, directory string) error {
	data, err := json.Marshal(config)
	if err != nil || len(data)+1 > 64<<10 {
		return errors.New("Server service configuration exceeds its bounded format")
	}
	if config.Schema != 1 || config.DataDir != directory || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || !utf8.ValidString(directory) || strings.ContainsAny(directory, "\x00\r\n") {
		return errors.New("Server service configuration requires its exact canonical data directory")
	}
	for _, value := range config.Arguments {
		if !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("Invalid Server service arguments")
		}
	}
	cmd, err := parseServe(config.Arguments)
	if err != nil || cmd.kind != commandServe || cmd.options.dataDir != directory {
		return errors.New("Invalid Server service arguments")
	}
	for key, value := range config.Environment {
		if !serverServiceEnvironment[key] || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("Invalid Server service environment")
		}
	}
	return nil
}

func installServerService(ctx context.Context, manager macService, config serverServiceConfig, binary string) error {
	if err := validateServerServiceConfig(config, manager.directory); err != nil {
		return err
	}
	// Stage public plist validation and known metadata checks before server.json
	// is replaced. A rejected path/environment cannot leave unreadable settings.
	if _, err := macServicePlistFor("server", binary, manager.directory, manager.arguments); err != nil {
		return err
	}
	for _, name := range []string{filepath.Join(manager.directory, "service", manager.label()+".plist"), filepath.Join(manager.loginDirectory, manager.label()+".plist")} {
		if _, err := readManagedMacPlist(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	targets, err := manager.loadedTargets(ctx)
	if err != nil {
		return err
	}
	if len(targets) != 0 {
		return errors.New("Stop this Server service before updating its installation")
	}
	ownership, err := serviceowner.Acquire(manager.directory)
	if err != nil {
		return err
	}
	defer ownership.Close()
	if err := writeServerServiceConfig(manager.directory, config); err != nil {
		return err
	}
	return manager.apply(ctx, "install", binary)
}

func writeServerServiceConfig(directory string, config serverServiceConfig) error {
	if err := validateServerServiceConfig(config, directory); err != nil {
		return err
	}
	path := filepath.Join(directory, "service")
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return errors.New("Server service configuration requires a canonical directory")
	}
	info, err := os.Stat(path)
	owner, ownerErr := serviceDataOwner(path)
	if err != nil || ownerErr != nil || info.Mode().Perm()&0077 != 0 || owner != uint32(os.Geteuid()) {
		return errors.New("Server service configuration requires a private owned directory")
	}
	name := filepath.Join(path, "server.json")
	if _, err := os.Lstat(name); err == nil {
		if _, err := readServerServiceConfig(directory); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(path, ".server-service-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = json.NewEncoder(file).Encode(config)
	err = errors.Join(err, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), name)
}

func runServerService(ctx context.Context, args []string, output io.Writer, _ string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("Server background services currently require macOS launchd; foreground serve remains available")
	}
	if len(args) == 0 {
		return errors.New("Choose service install, start, stop, status, enable-login, disable-login or remove")
	}
	action := args[0]
	if action == "help" || action == "--help" {
		_, err := fmt.Fprintln(output, "Usage: camp service install --data-dir PATH [serve options]\n       camp service start|stop|status|enable-login|disable-login|remove --data-dir PATH\nStatus accepts --json. Installation starts nothing; login startup is a separate choice.")
		return err
	}
	if os.Geteuid() == 0 {
		return errors.New("Manage the Mac Server as its original unprivileged account, without sudo")
	}
	var options serveOptions
	jsonOutput := false
	if action == "install" {
		cmd, err := parseServe(args[1:])
		if err != nil {
			return err
		}
		if cmd.kind != commandServe {
			return errors.New("Service install requires serve options")
		}
		options = cmd.options
	} else {
		switch action {
		case "start", "stop", "status", "enable-login", "disable-login", "remove", "run":
		default:
			return fmt.Errorf("Unknown Server service action %q", action)
		}
		flags := flag.NewFlagSet("service "+action, flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		flags.StringVar(&options.dataDir, "data-dir", "", "private Server data directory")
		if action == "status" {
			flags.BoolVar(&jsonOutput, "json", false, "structured service state")
		}
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("Service commands accept no positional arguments")
		}
	}
	if options.dataDir == "" {
		return errors.New("Server service commands require an explicit --data-dir")
	}
	directory, err := filepath.Abs(options.dataDir)
	if err != nil {
		return err
	}
	if action == "install" {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return err
		}
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	options.dataDir = directory
	info, err := os.Stat(directory)
	owner, ownerErr := serviceDataOwner(directory)
	if err != nil || ownerErr != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || owner != uint32(os.Geteuid()) {
		return errors.New("Server services require the original account's private data directory")
	}
	if action == "run" {
		return runServerServiceProcess(ctx, directory)
	}
	if action == "status" {
		return printServerServiceStatus(ctx, directory, output, jsonOutput)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	manager := macService{role: "server", domain: macServiceDomain(ctx), otherDomains: []string{"gui/" + strconv.Itoa(os.Geteuid()), "user/" + strconv.Itoa(os.Geteuid())}, directory: directory, loginDirectory: filepath.Join(home, "Library", "LaunchAgents"), arguments: []string{"service", "run", "--data-dir", directory}, lookup: lookupMacService, command: func(ctx context.Context, args ...string) error {
		cmd := exec.CommandContext(ctx, "/bin/launchctl", args...)
		cmd.Stdout, cmd.Stderr = output, output
		return cmd.Run()
	}}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return err
	}
	if action == "install" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		config, err := serverServiceSettings(options, os.Getenv, cwd)
		if err != nil {
			return err
		}
		if err := installServerService(ctx, manager, config, binary); err != nil {
			return err
		}
	} else if err := manager.apply(ctx, action, binary); err != nil {
		return err
	}

	if action == "remove" {
		if _, err := readServerServiceConfig(directory); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(directory, "service", "server.json")); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(output, "Server service %s completed. Team data was preserved.\n", action)
	if action == "install" {
		_, err = fmt.Fprintln(output, "Installed without starting or enabling login startup. Choose service start and service enable-login explicitly.")
	}
	return err
}

func runServerServiceProcess(ctx context.Context, directory string) error {
	config, err := readServerServiceConfig(directory)
	if err != nil {
		return err
	}
	cmd, err := parseServe(config.Arguments)
	if err != nil {
		return err
	}
	stdout, err := servicelog.Open(filepath.Join(directory, "logs"), "server.log")
	if err != nil {
		return err
	}
	defer stdout.Close()
	stderr, err := servicelog.Open(filepath.Join(directory, "logs"), "server-error.log")
	if err != nil {
		return err
	}
	defer stderr.Close()
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	err = runServe(ctx, cmd.options, stdout, func(name string) string { return config.Environment[name] }, logger)
	if err != nil {
		logger.Error("Server stopped", "error", err)
	}
	return err
}

func printServerServiceStatus(ctx context.Context, directory string, output io.Writer, jsonOutput bool) error {
	status := readMacServiceStatus(ctx, directory, "server")
	if status.Installed {
		if _, err := readServerServiceConfig(directory); err != nil {
			status.Reason = "Saved service settings are missing or invalid. Inspect the private settings and use foreground serve with explicit configuration for diagnostics before reinstalling."
		}
	}
	if jsonOutput {
		return json.NewEncoder(output).Encode(status)
	}
	_, err := fmt.Fprintf(output, "Server service: %s\nInstalled: %t\nLoaded: %t\nRunning: %t\nLogin startup: %t\n", status.State, status.Installed, status.Loaded, status.Running, status.LoginEnabled)
	if err == nil && status.Reason != "" {
		_, err = fmt.Fprintln(output, status.Reason)
	}
	return err
}
