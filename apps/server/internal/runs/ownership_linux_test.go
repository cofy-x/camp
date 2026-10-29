//go:build linux

package runs

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestOwnershipTransferIsSingleUseAndSurvivesCallerClose(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	owner, err := AcquireOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	store, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := NewWithOwnership(ctx, store, owner, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(ctx)
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if other, err := NewWithOwnership(ctx, store, owner, Config{}, nil); err == nil {
		_ = other.Shutdown(ctx)
		t.Fatal("An ownership token initialized more than one manager")
	}
	if contender, err := AcquireOwnership(dir); err == nil {
		_ = contender.Close()
		t.Fatal("Closing the transferred token released manager ownership")
	}
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := AcquireOwnership(dir)
	if err != nil {
		t.Fatalf("Manager did not release ownership: %v", err)
	}
	_ = next.Close()
}

func TestManagerInitializationFailureReleasesTransferredOwnership(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	owner, err := AcquireOwnership(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	store, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if manager, err := NewWithOwnership(ctx, store, owner, Config{}, nil); err == nil {
		_ = manager.Shutdown(ctx)
		t.Fatal("Initialization accepted unavailable storage")
	}
	next, err := AcquireOwnership(dir)
	if err != nil {
		t.Fatalf("Failed initialization leaked ownership: %v", err)
	}
	_ = next.Close()
}
