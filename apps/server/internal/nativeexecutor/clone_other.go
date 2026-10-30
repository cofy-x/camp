//go:build !darwin

package nativeexecutor

import "os"

func cloneDependencyFile(string, string, os.FileMode) (bool, error) { return false, nil }
