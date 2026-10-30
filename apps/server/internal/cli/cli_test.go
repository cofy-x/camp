package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want command
	}{
		{name: "default", want: command{kind: commandServe}},
		{name: "serve", args: []string{"serve"}, want: command{kind: commandServe}},
		{name: "help", args: []string{"help"}, want: command{kind: commandHelp}},
		{name: "help flag", args: []string{"--help"}, want: command{kind: commandHelp}},
		{name: "short help", args: []string{"-h"}, want: command{kind: commandHelp}},
		{name: "serve help", args: []string{"serve", "--help"}, want: command{kind: commandServeHelp}},
		{name: "serve short help", args: []string{"serve", "-h"}, want: command{kind: commandServeHelp}},
		{name: "version", args: []string{"version"}, want: command{kind: commandVersion}},
		{name: "version flag", args: []string{"--version"}, want: command{kind: commandVersion}},
		{
			name: "separate values", args: []string{"serve", "--addr", "127.0.0.1:4000", "--data-dir", "./local data"},
			want: command{options: serveOptions{addr: "127.0.0.1:4000", dataDir: "./local data"}},
		},
		{
			name: "equals values", args: []string{"serve", "--addr=[::1]:4000", "--data-dir=./local data"},
			want: command{options: serveOptions{addr: "[::1]:4000", dataDir: "./local data"}},
		},
		{name: "policy options", args: []string{"serve", "--allowed-hosts", "desk.example,192.0.2.1", "--trusted-origins", "http://localhost:5173"}, want: command{options: serveOptions{allowedHosts: "desk.example,192.0.2.1", trustedOrigins: "http://localhost:5173"}}},
		{name: "env file", args: []string{"serve", "--env-file", "./private config"}, want: command{options: serveOptions{envFile: "./private config"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCommand(tt.args)
			if err != nil || got != tt.want {
				t.Fatalf("parseCommand(%q) = %+v, %v; want %+v", tt.args, got, err, tt.want)
			}
		})
	}
}

func TestInvalidCommands(t *testing.T) {
	for _, args := range [][]string{
		{"unknown"}, {"--unknown"}, {"--addr", "127.0.0.1:4000"},
		{"help", "extra"}, {"--help", "extra"}, {"version", "extra"}, {"--version", "extra"},
		{"serve", "--unknown"}, {"serve", "--version"}, {"serve", "--help", "--unknown"},
		{"serve", "--addr"}, {"serve", "--data-dir"},
		{"serve", "--allowed-hosts="}, {"serve", "--trusted-origins="}, {"serve", "--allowed-hosts"}, {"serve", "--trusted-origins"},
		{"serve", "--addr="}, {"serve", "--data-dir="}, {"serve", "--data-dir", "  "},
		{"serve", "--env-file="}, {"serve", "--env-file"}, {"serve", "--env-file", "--help"},
		{"serve", "--addr", "--help"}, {"serve", "--data-dir", "--help"},
		{"serve", "extra"}, {"serve", "--", "extra"}, {"serve", "extra", "--help"},
		{"serve", "--help=invalid"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output bytes.Buffer
			if err := Run(context.Background(), args, &output, "dev"); err == nil || err.Error() == "" {
				t.Fatalf("run(%q) must return a readable error, got %v", args, err)
			}
			if output.Len() != 0 {
				t.Fatalf("invalid command wrote success output: %q", output.String())
			}
		})
	}
}

func TestInformationalCommandsHaveNoStorageSideEffects(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "must-not-exist")
	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("ADDR", "invalid-address")
	t.Setenv("PORT", "invalid-port")
	t.Setenv("CAMP_ENV_FILE", filepath.Join(t.TempDir(), "missing-private-env"))
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"help"}, "Usage: " + appName},
		{[]string{"--help"}, "Usage: " + appName},
		{[]string{"serve", "--help"}, "selected env file > managed Settings > defaults"},
		{[]string{"serve", "--addr", "invalid", "--data-dir", dataDir, "--help"}, "Usage: " + appName + " serve"},
		{[]string{"serve", "--env-file", "missing-file", "--help"}, "Usage: " + appName + " serve"},
		{[]string{"runner", "--help"}, "--development-runtime Use installed source runtimes (contributors only)"},
		{[]string{"runner", "setup", "--help"}, "Native Runner supports macOS 15+ on Apple Silicon"},
		{[]string{"runner", "service", "--help"}, "Usage: camp runner"},
		{[]string{"version"}, appName + " test-version\n"},
		{[]string{"--version"}, appName + " test-version\n"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var output bytes.Buffer
			if err := Run(context.Background(), tt.args, &output, "test-version"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), tt.want) {
				t.Fatalf("output = %q, want %q", output.String(), tt.want)
			}
			if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
				t.Fatalf("informational command created data directory: %v", err)
			}
		})
	}
}
