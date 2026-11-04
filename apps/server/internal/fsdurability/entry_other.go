//go:build !darwin

package fsdurability

import "os"

func Entry(file *os.File) error { return file.Sync() }
