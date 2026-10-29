package cli

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/server"
)

func TestConfigurationPrecedence(t *testing.T) {
	base := filepath.Join(t.TempDir(), "config")
	defaultData := filepath.Join(base, appName)
	for _, tt := range []struct {
		name    string
		options serveOptions
		env     map[string]string
		want    server.Config
	}{
		{name: "defaults", want: server.Config{Addr: "127.0.0.1:3001", DataDir: defaultData}},
		{name: "empty environment", env: map[string]string{"ADDR": "", "PORT": "", "DATA_DIR": ""}, want: server.Config{Addr: "127.0.0.1:3001", DataDir: defaultData}},
		{name: "port fallback", env: map[string]string{"PORT": "4000"}, want: server.Config{Addr: "127.0.0.1:4000", DataDir: defaultData}},
		{name: "address overrides port", env: map[string]string{"ADDR": "localhost:4001", "PORT": "invalid"}, want: server.Config{Addr: "localhost:4001", DataDir: defaultData}},
		{name: "environment data", env: map[string]string{"DATA_DIR": "./env data"}, want: server.Config{Addr: "127.0.0.1:3001", DataDir: "./env data"}},
		{
			name: "flags override environment", options: serveOptions{addr: "[::1]:4002", dataDir: "./flag data"},
			env:  map[string]string{"ADDR": "invalid", "PORT": "invalid", "DATA_DIR": "./env data"},
			want: server.Config{Addr: "[::1]:4002", DataDir: "./flag data"},
		},
		{name: "partial address override", options: serveOptions{addr: ":4003"}, env: map[string]string{"DATA_DIR": "./env"}, want: server.Config{Addr: ":4003", DataDir: "./env"}},
		{name: "partial data override", options: serveOptions{dataDir: "./flag"}, env: map[string]string{"PORT": "4004"}, want: server.Config{Addr: "127.0.0.1:4004", DataDir: "./flag"}},
		{name: "ephemeral port", options: serveOptions{addr: "127.0.0.1:0"}, want: server.Config{Addr: "127.0.0.1:0", DataDir: defaultData}},
		{name: "named port remains supported", options: serveOptions{addr: "localhost:http"}, want: server.Config{Addr: "localhost:http", DataDir: defaultData}},
		{name: "policy environment", env: map[string]string{"ALLOWED_HOSTS": "desk.example, 192.0.2.1", "TRUSTED_ORIGINS": "http://localhost:5173"}, want: server.Config{Addr: "127.0.0.1:3001", DataDir: defaultData, AllowedHosts: []string{"desk.example", "192.0.2.1"}, TrustedOrigins: []string{"http://localhost:5173"}}},
		{name: "policy flags override environment", options: serveOptions{allowedHosts: "desk.example", trustedOrigins: "http://localhost:5174"}, env: map[string]string{"ALLOWED_HOSTS": "other.example", "TRUSTED_ORIGINS": "http://localhost:5173"}, want: server.Config{Addr: "127.0.0.1:3001", DataDir: defaultData, AllowedHosts: []string{"desk.example"}, TrustedOrigins: []string{"http://localhost:5174"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			configCalls := 0
			got, err := resolveConfig(tt.options, func(key string) string { return tt.env[key] }, func() (string, error) {
				configCalls++
				return base, nil
			})
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("resolveConfig = %+v, %v; want %+v", got, err, tt.want)
			}
			wantCalls := 0
			if tt.want.DataDir == defaultData {
				wantCalls = 1
			}
			if configCalls != wantCalls {
				t.Errorf("config directory queried %d times, want %d", configCalls, wantCalls)
			}
		})
	}
}

func TestConfigurationErrors(t *testing.T) {
	missingHome := errors.New("no user configuration directory")
	_, err := resolveConfig(serveOptions{}, func(string) string { return "" }, func() (string, error) { return "", missingHome })
	if !errors.Is(err, missingHome) {
		t.Fatalf("missing config directory error = %v", err)
	}
	for _, addr := range []string{"localhost", "localhost:", "localhost:-1", "localhost:65536", "localhost:not-a-port"} {
		t.Run(addr, func(t *testing.T) {
			_, err := resolveConfig(serveOptions{addr: addr}, func(string) string { return "" }, func() (string, error) {
				t.Fatal("invalid address should fail before resolving the data directory")
				return "", nil
			})
			if err == nil || !strings.Contains(err.Error(), "invalid listen address") {
				t.Fatalf("invalid address error = %v", err)
			}
		})
	}
	_, err = resolveConfig(serveOptions{}, func(key string) string {
		if key == "PORT" {
			return "invalid"
		}
		return ""
	}, func() (string, error) { return "", missingHome })
	if err == nil || !strings.Contains(err.Error(), "invalid listen address") {
		t.Fatalf("invalid PORT error = %v", err)
	}
}

func TestPolicyListsRejectEmptyItems(t *testing.T) {
	for _, key := range []string{"ALLOWED_HOSTS", "TRUSTED_ORIGINS"} {
		_, err := resolveConfig(serveOptions{}, func(name string) string {
			if name == key {
				return "localhost,,other"
			}
			return ""
		}, func() (string, error) { return t.TempDir(), nil })
		if err == nil {
			t.Fatalf("accepted empty item in %s", key)
		}
	}
}
