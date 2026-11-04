package cli

import (
	"os"

	"github.com/cofy-x/camp/apps/server/internal/fsdurability"
)

// Stage completion retains Go's platform-specific full Sync semantics. Every
// entry has already been synchronized and closed before this directory opens.
func syncRuntimeStage(directory string) error {
	return fsdurability.Stage(directory)
}

func syncRuntimeEntry(file *os.File) error { return fsdurability.Entry(file) }
