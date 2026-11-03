//go:build !darwin && !linux

package servicelog

import (
	"errors"
	"os"
)

func owned(os.FileInfo) bool       { return false }
func ownedSingle(os.FileInfo) bool { return false }
func openPrivate(string, int) (*os.File, error) {
	return nil, errors.New("Unsupported service log platform")
}
