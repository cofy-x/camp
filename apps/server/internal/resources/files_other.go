//go:build !linux

package resources

import (
	"errors"
	"os"
)

func openRegular(root *os.Root, name string) (*os.File, error) {
	return nil, errors.New("Controlled project imports are not supported on this platform")
}

// Reject other platforms until file identity and hard-link checks have been verified.
func singleLink(info os.FileInfo) bool { return false }
