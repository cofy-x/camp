//go:build darwin

package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func TestMacServerServiceActualLaunchdAndRetainedData(t *testing.T) {
	if os.Getenv("CAMP_TEST_MAC_NATIVE") != "1" {
		t.Skip("requires actual Mac launchd qualification")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "team")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "camp")
	if output, err := exec.CommandContext(ctx, "go", "build", "-o", binary, "./testdata/mac-server-service-main.go").CombinedOutput(); err != nil {
		t.Fatalf("build %s %v", output, err)
	}
	config, err := serverServiceSettings(serveOptions{dataDir: directory, addr: "127.0.0.1:0"}, func(string) string { return "" }, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeServerServiceConfig(directory, config); err != nil {
		t.Fatal(err)
	}
	manager := macService{role: "server", domain: macServiceDomain(ctx), directory: directory, loginDirectory: filepath.Join(root, "login-fixture"), arguments: []string{"service", "run", "--data-dir", directory}, lookup: lookupMacService, command: func(ctx context.Context, args ...string) error {
		output, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("launchctl: %w %s", err, output)
		}
		return nil
	}}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		_ = manager.apply(cleanup, "remove", "")
	})
	if err := manager.apply(ctx, "install", binary); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "desktop.sqlite")); !os.IsNotExist(err) {
		t.Fatal("install started Server", err)
	}
	if err := manager.apply(ctx, "start", ""); err != nil {
		t.Fatal(err)
	}
	var address string
	pattern := regexp.MustCompile(`Open Camp: (http://127\.0\.0\.1:[0-9]+)`)
	for address == "" {
		data, _ := os.ReadFile(filepath.Join(directory, "logs", "server.log"))
		match := pattern.FindSubmatch(data)
		if len(match) == 2 {
			address = string(match[1])
			break
		}
		select {
		case <-ctx.Done():
			diagnostic, _ := os.ReadFile(filepath.Join(directory, "logs", "server-error.log"))
			t.Fatalf("Server not ready: %s", diagnostic)
		case <-time.After(30 * time.Millisecond):
		}
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(address + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	if err := manager.apply(ctx, "stop", ""); err != nil {
		t.Fatal(err)
	}
	if loaded, err := manager.isLoaded(ctx, manager.domain+"/"+manager.label()); err != nil || loaded {
		t.Fatal("Server remained loaded after stop")
	}
	data, err := os.ReadFile(filepath.Join(directory, "desktop.sqlite"))
	if err != nil || len(data) == 0 {
		t.Fatal("missing retained team database", err)
	}
	if err := manager.apply(ctx, "remove", ""); err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(filepath.Join(directory, "desktop.sqlite"))
	if err != nil || string(retained) != string(data) {
		t.Fatal("removing service changed team database", err)
	}
}
