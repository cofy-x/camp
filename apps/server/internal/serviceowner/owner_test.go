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
