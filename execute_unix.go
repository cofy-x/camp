//go:build linux || darwin

package main

import (
	"os"
	"syscall"
)

// Replace the launcher so exit status, terminal signals and service supervision
// belong to the actual Server/Runner rather than an intermediate process.
func execute(binary string, args []string) error {
	return syscall.Exec(binary, append([]string{binary}, args...), os.Environ())
}
