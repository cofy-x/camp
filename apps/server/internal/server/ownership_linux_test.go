//go:build linux

package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/runs"
)

func TestBlockedStartupDoesNotOpenStorage(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "no existing database"
		if existing {
			name = "existing database file"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			database := filepath.Join(dir, "desktop.sqlite")
			const sentinel = "Do not open or replace another owner's database"
			if existing {
				if err := os.WriteFile(database, []byte(sentinel), 0600); err != nil {
					t.Fatal(err)
				}
			}
			owner, err := runs.AcquireOwnership(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			urls := make(chan string, 1)
			err = Run(context.Background(), Config{Addr: "127.0.0.1:0", DataDir: dir, Logger: slog.New(readinessHandler{urls})})
			if err == nil || !strings.Contains(err.Error(), "ownership") {
				t.Fatalf("A competing startup did not stop at the ownership fence: %v", err)
			}
			if existing {
				if data, err := os.ReadFile(database); err != nil || string(data) != sentinel {
					t.Fatalf("Blocked startup changed existing data: %q %v", data, err)
				}
			} else if _, err := os.Stat(database); !os.IsNotExist(err) {
				t.Fatalf("Blocked startup created a database: %v", err)
			}
			for _, suffix := range []string{".migrate.lock", "-wal", "-shm"} {
				if _, err := os.Stat(database + suffix); !os.IsNotExist(err) {
					t.Fatalf("Blocked startup touched SQLite resources %s: %v", suffix, err)
				}
			}
			select {
			case url := <-urls:
				t.Fatalf("Blocked startup announced readiness: %s", url)
			default:
			}
		})
	}
}

func TestFreshStartupRetainsOwnershipUntilShutdown(t *testing.T) {
	dir := t.TempDir()
	urls := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		done <- Run(ctx, Config{Addr: "127.0.0.1:0", DataDir: dir, Logger: slog.New(readinessHandler{urls})})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
		}
	})
	select {
	case <-urls:
	case err := <-done:
		t.Fatalf("Fresh startup failed: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("Fresh startup did not finish")
	}
	if _, err := os.Stat(filepath.Join(dir, "desktop.sqlite")); err != nil {
		t.Fatalf("Fresh startup did not create storage: %v", err)
	}
	if contender, err := runs.AcquireOwnership(dir); err == nil {
		_ = contender.Close()
		t.Fatal("Startup released ownership instead of transferring it to the manager")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown did not finish")
	}
	next, err := runs.AcquireOwnership(dir)
	if err != nil {
		t.Fatalf("Shutdown leaked execution ownership: %v", err)
	}
	_ = next.Close()
}

func TestDatabaseStartupFailureReleasesExecutionOwnership(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "desktop.sqlite"), []byte("This is not a SQLite database"), 0600); err != nil {
		t.Fatal(err)
	}
	err := Run(context.Background(), Config{Addr: "127.0.0.1:0", DataDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err == nil || !strings.Contains(err.Error(), "open database") {
		t.Fatalf("Expected database startup failure: %v", err)
	}
	owner, err := runs.AcquireOwnership(dir)
	if err != nil {
		t.Fatalf("Database failure leaked execution ownership: %v", err)
	}
	_ = owner.Close()
}
