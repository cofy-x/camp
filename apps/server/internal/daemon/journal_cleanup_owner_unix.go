//go:build linux || darwin

package daemon

import (
	"os"
	"syscall"
)

func cleanupJournalOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
