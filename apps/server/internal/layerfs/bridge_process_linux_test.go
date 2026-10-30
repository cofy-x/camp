//go:build linux

package layerfs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBridgeInheritedLockOutlivesParentDescriptor(t *testing.T) {
	config, bridge := fixtureBridge(t)
	if err := bridge.lock.Close(); err != nil {
		t.Fatal(err)
	}
	if other, err := OpenBridge(context.Background(), config); err == nil {
		_ = other.Close()
		t.Fatal("the SDK process did not retain its inherited home lock")
	}
	_ = bridge.Close()
	other, err := OpenBridge(context.Background(), config)
	if err != nil {
		t.Fatalf("the home lock survived the SDK process: %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeParentProcessChild(t *testing.T) {
	configPath := os.Getenv("CAMP_BRIDGE_PARENT_TEST")
	if configPath == "" {
		return
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Node, RuntimeDir, DataRoot, Home string
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	bridge, err := OpenBridge(context.Background(), BridgeConfig{Node: config.Node, RuntimeDir: config.RuntimeDir, DataRoot: config.DataRoot, Home: config.Home})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Home, "parent-test-pid"), []byte(strconv.Itoa(bridge.cmd.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Minute)
	_ = bridge.Close()
}

func TestBridgeDiesWithParentBeforeHomeCanBeReused(t *testing.T) {
	config, bridge := fixtureBridge(t)
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	configuration := filepath.Join(config.DataRoot, "parent-test-config.json")
	data, err := json.Marshal(map[string]string{"Node": config.Node, "RuntimeDir": config.RuntimeDir, "DataRoot": config.DataRoot, "Home": config.Home})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configuration, data, 0600); err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(os.Args[0], "-test.run=^TestBridgeParentProcessChild$")
	parent.Env = append(os.Environ(), "CAMP_BRIDGE_PARENT_TEST="+configuration)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Process.Kill(); _ = parent.Wait() })
	var pid string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(config.Home, "parent-test-pid"))
		parsed, parseErr := strconv.Atoi(string(data))
		if err == nil && parseErr == nil && parsed > 1 {
			pid = string(data)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == "" {
		t.Fatal("test parent did not start the SDK process")
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	deadline = time.Now().Add(3 * time.Second)
	for {
		data, err := os.ReadFile(filepath.Join("/proc", pid, "stat"))
		ended := errors.Is(err, os.ErrNotExist)
		if index := strings.LastIndex(string(data), ")"); index >= 0 {
			ended = strings.HasPrefix(string(data[index+1:]), " Z ") || strings.HasPrefix(string(data[index+1:]), " X ")
		}
		if ended {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the SDK process survived its parent")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A zombie cannot perform SDK work, but the kernel may still be completing
	// deferred descriptor release. The inherited flock is the ownership
	// authority; wait for it rather than inferring release from process state.
	deadline = time.Now().Add(3 * time.Second)
	for {
		ownership, err := openBridgeLock(config.Home)
		if err == nil {
			_ = ownership.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the terminated SDK retained home ownership: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	other, err := OpenBridge(context.Background(), config)
	if err != nil {
		t.Fatalf("the terminated SDK retained home ownership: %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
}
