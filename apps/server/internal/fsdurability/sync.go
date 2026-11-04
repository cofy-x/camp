// Package fsdurability synchronizes private staging writes before publication.
// Callers own admission and must drain every writer before the final barrier.
package fsdurability

import (
	"errors"
	"os"
)

// Stage retains Go's platform-specific full synchronization semantics.
func Stage(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
