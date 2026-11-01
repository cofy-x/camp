//go:build !linux && !darwin

package daemon

import "os"

func cleanupJournalOwner(os.FileInfo) bool { return false }
