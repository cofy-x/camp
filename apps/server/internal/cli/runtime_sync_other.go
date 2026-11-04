//go:build !darwin

package cli

import "os"

func syncRuntimeEntry(file *os.File) error { return file.Sync() }
