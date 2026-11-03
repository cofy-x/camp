package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServerServiceSettingsFreezePublicConfigurationWithoutCredentials(t *testing.T) {
	directory, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ADDR": "127.0.0.1:3301", "CAMP_PUBLIC_ORIGIN": "https://camp.example", "CAMP_IMPORT_ROOTS": "/approved/source", "DEEPSEEK_API_KEY": "must-not-save", "HTTPS_PROXY": "must-not-inherit", "CAMP_DOCUMENT_NODE": "runtime/node"}
	config, err := serverServiceSettings(serveOptions{dataDir: directory, envFile: "private.env"}, func(k string) string { return env[k] }, directory)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "must-not") || strings.Contains(string(data), "DEEPSEEK") || strings.Contains(string(data), "HTTPS_PROXY") {
		t.Fatal("persisted ambient credentials or proxy")
	}
	if config.Environment["CAMP_DOCUMENT_NODE"] != filepath.Join(directory, "runtime/node") {
		t.Fatal(config.Environment)
	}
	if err := writeServerServiceConfig(directory, config); err != nil {
		t.Fatal(err)
	}
	stored, err := readServerServiceConfig(directory)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(stored.Arguments, " ") != strings.Join(config.Arguments, " ") {
		t.Fatal("configuration changed")
	}
	cmd, err := parseServe(stored.Arguments)
	if err != nil || cmd.options.envFile != filepath.Join(directory, "private.env") {
		t.Fatal(cmd, err)
	}
}

func TestServerServiceRefusesMovedConfigurationAndUnknownEnvironment(t *testing.T) {
	for _, move := range []bool{false, true} {
		t.Run(map[bool]string{true: "relocated", false: "ambient-environment"}[move], func(t *testing.T) {
			directory, _ := filepath.EvalSymlinks(t.TempDir())
			_ = os.Chmod(directory, 0700)
			config, err := serverServiceSettings(serveOptions{dataDir: directory}, func(string) string { return "" }, directory)
			if err != nil {
				t.Fatal(err)
			}
			if move {
				config.DataDir = filepath.Join(directory, "another")
			} else {
				config.Environment["DEEPSEEK_API_KEY"] = "private"
			}
			if err := writeServerServiceConfig(directory, config); err == nil {
				t.Fatal("Persisted invalid generated service configuration")
			}
			// A separately corrupted historical file is rejected on read too.
			raw, _ := json.Marshal(config)
			if err := os.MkdirAll(filepath.Join(directory, "service"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "service", "server.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readServerServiceConfig(directory); err == nil {
				t.Fatal("Accepted invalid historical service configuration")
			}
		})
	}
}

func TestServerServiceSharesLifecycleWithoutRunnerIdentity(t *testing.T) {
	directory, _ := filepath.EvalSymlinks(t.TempDir())
	_ = os.Chmod(directory, 0700)
	retained := filepath.Join(directory, "desktop.sqlite")
	if err := os.WriteFile(retained, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	var commands [][]string
	active := false
	manager := macService{role: "server", domain: "gui/501", directory: directory, loginDirectory: t.TempDir(), arguments: []string{"service", "run", "--data-dir", directory}, loaded: func(context.Context, string) bool { return active }, command: func(_ context.Context, args ...string) error {
		commands = append(commands, args)
		if args[0] == "bootstrap" {
			active = true
		}
		if args[0] == "bootout" {
			active = false
		}
		return nil
	}}
	ctx := context.Background()
	if err := manager.apply(ctx, "install", "/absolute/Camp & Tools/camp"); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 0 {
		t.Fatal("installation started service")
	}
	serverLabel := manager.label()
	if serverLabel == macServiceLabel(directory) {
		t.Fatal("Server and Runner identity collided")
	}
	data, err := readManagedMacPlist(filepath.Join(directory, "service", serverLabel+".plist"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Managed by Camp Server") || !strings.Contains(string(data), "<string>service</string><string>run</string>") || strings.Contains(string(data), "runner</string>") {
		t.Fatal("wrong service command")
	}
	for _, action := range []string{"enable-login", "start", "stop", "disable-login", "remove"} {
		if err := manager.apply(ctx, action, ""); err != nil {
			t.Fatal(action, err)
		}
	}
	if len(commands) != 2 || commands[0][0] != "bootstrap" || commands[1][0] != "bootout" {
		t.Fatal(commands)
	}
	data, err = os.ReadFile(retained)
	if err != nil || string(data) != "retained" {
		t.Fatal("team data changed", err)
	}
}

func TestRejectedServerInstallationPreservesSettingsAndAllowsValidRetry(t *testing.T) {
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	manager := macService{role: "server", directory: directory, domain: "gui/501", loginDirectory: t.TempDir(), arguments: []string{"service", "run", "--data-dir", directory}, loaded: func(context.Context, string) bool { return false }, command: func(context.Context, ...string) error {
		t.Fatal("Installation unexpectedly started service")
		return nil
	}}
	config, err := serverServiceSettings(serveOptions{dataDir: directory, addr: "127.0.0.1:3001"}, func(string) string { return "" }, directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = installServerService(context.Background(), manager, config, "/private/camp"); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(directory, "service", "server.json")
	plist := filepath.Join(directory, "service", manager.label()+".plist")
	beforeConfig, _ := os.ReadFile(settings)
	beforePlist, _ := os.ReadFile(plist)
	for _, mutate := range []func(*serverServiceConfig){
		func(c *serverServiceConfig) {
			c.Environment = map[string]string{"CAMP_PUBLIC_ORIGIN": "https://camp.example\nprivate-marker"}
		},
		func(c *serverServiceConfig) {
			c.Environment = map[string]string{"CAMP_IMPORT_ROOTS": strings.Repeat("x", 64<<10)}
		},
		func(c *serverServiceConfig) { c.Environment = map[string]string{"DEEPSEEK_API_KEY": "private-marker"} },
		func(c *serverServiceConfig) {
			c.Arguments = append(append([]string{}, c.Arguments...), "--env-file", "/private/config\nsecret")
		},
		func(c *serverServiceConfig) { c.DataDir = filepath.Join(directory, "other") },
	} {
		invalid := config
		mutate(&invalid)
		if err = installServerService(context.Background(), manager, invalid, "/private/new-camp"); err == nil {
			t.Fatal("Invalid installation was accepted")
		}
		afterConfig, _ := os.ReadFile(settings)
		afterPlist, _ := os.ReadFile(plist)
		if !bytes.Equal(beforeConfig, afterConfig) || !bytes.Equal(beforePlist, afterPlist) {
			t.Fatal("Rejected settings changed existing installation")
		}
	}
	if err = installServerService(context.Background(), manager, config, "/private/camp\ninvalid"); err == nil {
		t.Fatal("Invalid plist path was accepted")
	}
	afterConfig, _ := os.ReadFile(settings)
	afterPlist, _ := os.ReadFile(plist)
	if !bytes.Equal(beforeConfig, afterConfig) || !bytes.Equal(beforePlist, afterPlist) {
		t.Fatal("Rejected plist changed configuration")
	}
	next, err := serverServiceSettings(serveOptions{dataDir: directory, addr: "127.0.0.1:3002"}, func(string) string { return "" }, directory)
	if err != nil {
		t.Fatal(err)
	}
	manager.lookup = func(context.Context, string) (bool, error) { return false, errors.New("uncertain service lookup") }
	if err = installServerService(context.Background(), manager, next, "/private/new-camp"); err == nil {
		t.Fatal("Uncertain lookup changed service settings")
	}
	afterConfig, _ = os.ReadFile(settings)
	afterPlist, _ = os.ReadFile(plist)
	if !bytes.Equal(beforeConfig, afterConfig) || !bytes.Equal(beforePlist, afterPlist) {
		t.Fatal("Uncertain lookup changed previous settings")
	}
	manager.lookup = nil
	if err = installServerService(context.Background(), manager, next, "/private/new-camp"); err != nil {
		t.Fatal("Rejected attempt prevented a valid retry", err)
	}
	stored, err := readServerServiceConfig(directory)
	if err != nil || strings.Join(stored.Arguments, " ") == strings.Join(config.Arguments, " ") {
		t.Fatal("Valid explicit retry did not update settings", err)
	}
}

func TestGeneratedServiceSettingsRejectInvalidEnvironmentBeforeWriting(t *testing.T) {
	directory, _ := filepath.EvalSymlinks(t.TempDir())
	for _, value := range []string{"https://camp.example\nprivate-marker", strings.Repeat("x", 64<<10), "https://camp.example\x00private"} {
		if _, err := serverServiceSettings(serveOptions{dataDir: directory}, func(key string) string {
			if key == "CAMP_PUBLIC_ORIGIN" {
				return value
			}
			return ""
		}, directory); err == nil {
			t.Fatal("Generated unreadable service settings")
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "service")); !os.IsNotExist(err) {
		t.Fatal("Pure settings validation wrote service files", err)
	}
}

func TestServerServiceStatusExplainsInvalidConfigWithoutPrivateValues(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS local service diagnostics")
	}
	directory, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	content, err := macServicePlistFor("server", "/private/camp", directory, []string{"service", "run", "--data-dir", directory})
	if err != nil {
		t.Fatal(err)
	}
	if err = writeMacPlist(filepath.Join(directory, "service", macServiceLabelFor("server", directory)+".plist"), content); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "service", "server.json"), []byte(`{"private-value":"must-not-display"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = printServerServiceStatus(context.Background(), directory, &output, true); err != nil {
		t.Fatal(err)
	}
	var status serviceStatus
	if err = json.Unmarshal(output.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Installed || !strings.Contains(status.Reason, "foreground serve") || strings.Contains(output.String(), "must-not-display") {
		t.Fatal("Invalid settings had no controlled diagnostic", output.String())
	}
}
