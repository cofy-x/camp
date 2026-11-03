package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
			if err := writeServerServiceConfig(directory, config); err != nil {
				t.Fatal(err)
			}
			if _, err := readServerServiceConfig(directory); err == nil {
				t.Fatal("accepted invalid service configuration")
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
	manager := macRunnerService{role: "server", domain: "gui/501", directory: directory, loginDirectory: t.TempDir(), arguments: []string{"service", "run", "--data-dir", directory}, loaded: func(context.Context, string) bool { return active }, command: func(_ context.Context, args ...string) error {
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
