package cli

import (
	"errors"
	"os"
)

// Stage completion retains Go's platform-specific full Sync semantics. Every
// entry has already been synchronized and closed before this directory opens.
func syncRuntimeStage(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
