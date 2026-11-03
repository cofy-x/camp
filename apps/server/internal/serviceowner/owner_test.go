package serviceowner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOwnershipKeepsTheExistingLockAndTransfersOnlyOnce(t *testing.T) {
	root := t.TempDir()
	owner, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, err = os.Stat(filepath.Join(root, "runs", ".lock")); err != nil {
		t.Fatal("Changed the deployed lock path", err)
	}
	if other, err := Acquire(root); err == nil {
		other.Close()
		t.Fatal("Two owners acquired the same directory")
	} else if !errors.Is(err, ErrDirectoryInUse) {
		t.Fatal(err)
	}
	_, fd, err := owner.Take()
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	if _, _, err = owner.Take(); err == nil {
		t.Fatal("Transferred ownership twice")
	}
	if err = owner.Close(); err != nil {
		t.Fatal(err)
	}
	if other, err := Acquire(root); err == nil {
		other.Close()
		t.Fatal("Closing transferred ownership released the FD")
	}
	if err = fd.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
}

func TestOwnershipRejectsLinkedRunDirectoryAndLockWithoutTouchingTargets(t *testing.T) {
	for _, linked := range []string{"directory", "lock"} {
		t.Run(linked, func(t *testing.T) {
			root, external := t.TempDir(), t.TempDir()
			runs := filepath.Join(root, "runs")
			if linked == "directory" {
				if err := os.Symlink(external, runs); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(runs, 0700); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(external, "retained-lock")
				if err := os.WriteFile(target, []byte("Preserve the external file."), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(runs, ".lock")); err != nil {
					t.Fatal(err)
				}
			}
			for _, acquire := range []func(string) (*Ownership, error){Acquire, AcquireExisting} {
				if owner, err := acquire(root); err == nil {
					owner.Close()
					t.Fatal("a linked ownership path was accepted")
				}
			}
			entries, err := os.ReadDir(external)
			if err != nil {
				t.Fatal(err)
			}
			if linked == "directory" && len(entries) != 0 {
				t.Fatal("rejected ownership created an external lock")
			}
			if linked == "lock" {
				data, err := os.ReadFile(filepath.Join(external, "retained-lock"))
				if err != nil || string(data) != "Preserve the external file." {
					t.Fatal("rejected ownership changed an external file", err)
				}
			}
		})
	}
}

func TestDataDirectoryAliasUsesTheSameOwnershipFence(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "server-data")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	owner, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	for _, acquire := range []func(string) (*Ownership, error){Acquire, AcquireExisting} {
		if other, err := acquire(alias); err == nil {
			other.Close()
			t.Fatal("a data-directory alias bypassed the running Server's fence")
		} else if !errors.Is(err, ErrDirectoryInUse) {
			t.Fatal("a real ownership directory became inaccessible through its data-directory alias", err)
		}
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := AcquireExisting(alias)
	if err != nil {
		t.Fatal("a stopped data-directory alias could not retain its existing lock", err)
	}
	restored.Close()
}
