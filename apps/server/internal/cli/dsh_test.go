package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func privateEnv(t *testing.T, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDSHConfigDefaultsAndEnvironmentAllowlist(t *testing.T) {
	cwd := t.TempDir()
	accessed := make(map[string]bool)
	config, err := readDSHConfig(serveOptions{}, func(key string) string {
		accessed[key] = true
		return ""
	}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if config.APIKey != "" || config.Model != "deepseek-v4-pro" || config.BaseURL != "https://api.deepseek.com/anthropic" || config.RuntimeDir != filepath.Join(cwd, "tools", "dsh-runtime") || config.MaxTokens != 4096 || config.MaxSteps != 8 || config.Timeout != 180*time.Second {
		t.Fatal("DSH defaults do not match the documented configuration")
	}
	delete(accessed, "CAMP_ENV_FILE")
	delete(accessed, "CAMP_DOCKER_SOCKET")
	if len(accessed) != len(dshKeys) {
		t.Fatalf("read %d process variables, expected %d", len(accessed), len(dshKeys))
	}
	for _, key := range dshKeys {
		if !accessed[key] {
			t.Errorf("did not resolve supported key %s", key)
		}
	}
}

func TestDSHConfigPrecedence(t *testing.T) {
	cwd := t.TempDir()
	privateEnv(t, filepath.Join(cwd, ".env"), "DEEPSEEK_API_KEY=default-file-value\nCAMP_DSH_MAX_STEPS=2\n")
	privateEnv(t, filepath.Join(cwd, "selected.env"), "DEEPSEEK_API_KEY=selected-file-value\nCAMP_DSH_MAX_STEPS=3\nCAMP_DSH_RUNTIME=local-runtime\n")
	privateEnv(t, filepath.Join(cwd, "explicit.env"), "DEEPSEEK_API_KEY=explicit-file-value\nCAMP_DSH_MAX_STEPS=4\nCAMP_DSH_MAX_TOKENS=1024\nCAMP_DSH_TIMEOUT_SECONDS=30\nCAMP_DSH_MODEL=test-model\nCAMP_DSH_NODE=node-custom\nDEEPSEEK_BASE_URL=https://model.invalid/v1\n")
	for _, test := range []struct {
		name    string
		options serveOptions
		env     map[string]string
		wantKey string
		steps   int
	}{
		{name: "default file", wantKey: "default-file-value", steps: 2},
		{name: "env selects file", env: map[string]string{"CAMP_ENV_FILE": "selected.env"}, wantKey: "selected-file-value", steps: 3},
		{name: "flag selects file", options: serveOptions{envFile: "explicit.env"}, env: map[string]string{"CAMP_ENV_FILE": "missing.env"}, wantKey: "explicit-file-value", steps: 4},
		{name: "process overrides file", options: serveOptions{envFile: "explicit.env"}, env: map[string]string{"DEEPSEEK_API_KEY": "process-value", "CAMP_DSH_MAX_STEPS": "6"}, wantKey: "process-value", steps: 6},
		{name: "real project budget", env: map[string]string{"CAMP_DSH_MAX_STEPS": "24"}, wantKey: "default-file-value", steps: 24},
		{name: "maximum step budget", env: map[string]string{"CAMP_DSH_MAX_STEPS": "32"}, wantKey: "default-file-value", steps: 32},
		{name: "empty process uses file", options: serveOptions{envFile: "explicit.env"}, env: map[string]string{"DEEPSEEK_API_KEY": "", "CAMP_DSH_MAX_STEPS": ""}, wantKey: "explicit-file-value", steps: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := readDSHConfig(test.options, func(key string) string { return test.env[key] }, cwd)
			if err != nil || config.APIKey != test.wantKey || config.MaxSteps != test.steps {
				t.Fatalf("configuration precedence failed: %v", err)
			}
			if test.options.envFile != "" && (config.NodePath != "node-custom" || config.Model != "test-model" || config.BaseURL != "https://model.invalid/v1" || config.MaxTokens != 1024 || config.Timeout != 30*time.Second) {
				t.Fatal("selected file fields were not resolved")
			}
			if test.env["CAMP_ENV_FILE"] == "selected.env" && config.RuntimeDir != filepath.Join(cwd, "local-runtime") {
				t.Fatal("relative runtime directory was not based on the service working directory")
			}
		})
	}
}

func TestDSHEnvParserReadsOnlyLiteralAllowlistedValues(t *testing.T) {
	input := `# private values
UNRELATED="a deliberately malformed unrelated variable
export DEEPSEEK_API_KEY = 'literal-$DO_NOT_EXPAND-$(do-not-run)' # comment
CAMP_DSH_MODEL="literal-model"
CAMP_DSH_NODE=/path/with space/node # comment
DEEPSEEK_BASE_URL=https://example.invalid/api#literal
CAMP_DSH_RUNTIME=
`
	values, err := parseDSHEnv(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"DEEPSEEK_API_KEY":  "literal-$DO_NOT_EXPAND-$(do-not-run)",
		"CAMP_DSH_MODEL":    "literal-model",
		"CAMP_DSH_NODE":     "/path/with space/node",
		"DEEPSEEK_BASE_URL": "https://example.invalid/api#literal",
		"CAMP_DSH_RUNTIME":  "",
	}
	if !reflect.DeepEqual(values, want) {
		t.Fatal("parser expanded or incorrectly interpreted literal values")
	}
}

func TestDSHEnvErrorsNeverIncludeValues(t *testing.T) {
	const secret = "private-sentinel-never-echo"
	for _, input := range []string{
		"DEEPSEEK_API_KEY '" + secret + "'",
		"DEEPSEEK_API_KEY=\"" + secret,
		"DEEPSEEK_API_KEY='" + secret + "' trailing",
		"DEEPSEEK_API_KEY=" + secret + "\nDEEPSEEK_API_KEY=duplicate",
		"DEEPSEEK_API_KEY=" + secret + "\x00",
	} {
		_, err := parseDSHEnv(strings.NewReader(input))
		if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "DEEPSEEK_API_KEY") || !strings.Contains(err.Error(), "line") {
			t.Fatalf("parser did not return a safe, located error: %v", err)
		}
	}
}

func TestDSHConfigAcceptsExplicitProjectTimeout(t *testing.T) {
	for _, seconds := range []int{600, 900} {
		config, err := readDSHConfig(serveOptions{}, func(key string) string {
			if key == "CAMP_DSH_TIMEOUT_SECONDS" {
				return fmt.Sprint(seconds)
			}
			return ""
		}, t.TempDir())
		if err != nil || config.Timeout != time.Duration(seconds)*time.Second {
			t.Fatalf("explicit project deadline was not retained: %v", err)
		}
	}
}

func TestDSHConfigRejectsUnsafeBudgetsWithoutEchoingValues(t *testing.T) {
	cwd := t.TempDir()
	for key, maximum := range map[string]string{"CAMP_DSH_MAX_TOKENS": "8193", "CAMP_DSH_MAX_STEPS": "33", "CAMP_DSH_TIMEOUT_SECONDS": "901"} {
		for _, value := range []string{"0", "-1", maximum, "secret-sentinel", "99999999999999999999999999999999"} {
			_, err := readDSHConfig(serveOptions{}, func(name string) string {
				if name == key {
					return value
				}
				return ""
			}, cwd)
			if err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "secret-sentinel") {
				t.Fatalf("budget validation did not safely identify %s: %v", key, err)
			}
		}
	}
	for _, value := range []string{"https://user:secret-sentinel@example.invalid", "https://example.invalid?secret-sentinel", "secret-sentinel", "https://example.invalid\nsecret-sentinel"} {
		_, err := readDSHConfig(serveOptions{}, func(key string) string {
			if key == "DEEPSEEK_BASE_URL" {
				return value
			}
			return ""
		}, cwd)
		if err == nil || !strings.Contains(err.Error(), "DEEPSEEK_BASE_URL") || strings.Contains(err.Error(), "secret-sentinel") {
			t.Fatalf("URL validation did not safely identify its key: %v", err)
		}
	}
}

func TestDSHEnvSelectionAndPrivacy(t *testing.T) {
	cwd := t.TempDir()
	if _, err := readDSHConfig(serveOptions{envFile: "missing"}, func(string) string { return "" }, cwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing explicit file: %v", err)
	}
	if _, err := readDSHConfig(serveOptions{}, func(key string) string {
		if key == "CAMP_ENV_FILE" {
			return "missing"
		}
		return ""
	}, cwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing environment-selected file: %v", err)
	}
	if _, err := readPrivateEnv(cwd, true); err == nil {
		t.Fatal("accepted a directory as a private env file")
	}
	filename := filepath.Join(cwd, ".env")
	privateEnv(t, filename, "DEEPSEEK_API_KEY=file-value\n")
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filename, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := readPrivateEnv(filename, true); err == nil || !strings.Contains(err.Error(), "owner") {
			t.Fatalf("public env file not rejected: %v", err)
		}
		if err := os.Chmod(filename, 0400); err != nil {
			t.Fatal(err)
		}
		if _, err := readPrivateEnv(filename, true); err != nil {
			t.Fatalf("private read-only env file rejected: %v", err)
		}
	}
}

func TestDSHConfigFailureDoesNotStartService(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "must-not-exist")
	filename := filepath.Join(t.TempDir(), "private.env")
	privateEnv(t, filename, "DEEPSEEK_API_KEY='private-sentinel\n")
	var output bytes.Buffer
	err := Run(context.Background(), []string{"serve", "--addr", "127.0.0.1:0", "--data-dir", dataDir, "--env-file", filename}, &output, "dev")
	if err == nil || strings.Contains(err.Error(), "private-sentinel") || output.Len() != 0 {
		t.Fatalf("configuration failure was not safely returned: %v", err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("configuration failure created service data: %v", err)
	}
}

func TestLayerFSConfigPathsAndOperatorOnlyDockerSocket(t *testing.T) {
	cwd := t.TempDir()
	privateEnv(t, filepath.Join(cwd, ".env"), "CAMP_LAYERFS_RUNTIME=private-runtime\nCAMP_SANDBOX_HELPER=private-helper\nCAMP_DOCKER_SOCKET=/untrusted/file.sock\n")
	config, err := readDSHConfig(serveOptions{}, func(key string) string {
		switch key {
		case "CAMP_SANDBOX_HELPER":
			return "operator-helper"
		case "CAMP_DOCKER_SOCKET":
			return "/operator/docker.sock"
		}
		return ""
	}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if config.LayerFSRuntimeDir != filepath.Join(cwd, "private-runtime") || config.SandboxHelper != filepath.Join(cwd, "operator-helper") || config.DockerSocket != "/operator/docker.sock" {
		t.Fatalf("wrong runtime config: %+v", config)
	}
	config, err = readDSHConfig(serveOptions{}, func(string) string { return "" }, cwd)
	if err != nil || config.DockerSocket != "" {
		t.Fatal("private model file selected Docker control socket")
	}
}
