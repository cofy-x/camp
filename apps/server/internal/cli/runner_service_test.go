package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNativeServiceDoesNotTakeOverAnotherUsersData(t *testing.T) {
	if err := validateServiceOwner("native", 1000); err == nil {
		t.Fatal("Native service accepted another account's private data")
	}
	if err := validateServiceOwner("native", 0); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerServiceActionsAreSeparateAndPreserveData(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private data")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(directory, "journal.sqlite")
	if err := os.WriteFile(journal, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	var commands [][]string
	manager := runnerService{directory: t.TempDir(), command: func(_ context.Context, args ...string) error {
		commands = append(commands, append([]string(nil), args...))
		return nil
	}}
	ctx := context.Background()
	if err := manager.apply(ctx, "install", "/opt/Camp $ Runner%/camp", directory); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(commands, [][]string{{"daemon-reload"}}) {
		t.Fatalf("Installation implicitly started/enabled service: %v", commands)
	}
	unit, err := os.ReadFile(filepath.Join(manager.directory, serviceName(directory)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), `"/opt/Camp $$ Runner%%/camp" runner run`) {
		t.Fatalf("Unescaped unit: %s", unit)
	}
	if !strings.Contains(string(unit), fmt.Sprintf("\nRestartPreventExitStatus=%d\n", ExitNeedsAttention)) || !strings.Contains(string(unit), "\nRestart=on-failure\n") {
		t.Fatalf("Service does not distinguish recoverable failure from required operator action: %s", unit)
	}
	for _, action := range []string{"start", "enable-boot", "disable-boot", "stop", "remove"} {
		if err := manager.apply(ctx, action, "", directory); err != nil {
			t.Fatal(err)
		}
	}
	if content, err := os.ReadFile(journal); err != nil || string(content) != "retained" {
		t.Fatal("Service removal changed Runner data", err)
	}
	if _, err := os.Stat(filepath.Join(manager.directory, serviceName(directory))); !os.IsNotExist(err) {
		t.Fatal("Service unit remained", err)
	}
}
func TestServiceRejectsInjectedOrRelativePaths(t *testing.T) {
	for _, value := range []string{"relative", "/opt/camp\nExecStart=/bad", "/opt/camp\x00"} {
		if _, err := serviceUnit(value, "/private/data"); err == nil {
			t.Fatal("Accepted unsafe service path", value)
		}
	}
}
