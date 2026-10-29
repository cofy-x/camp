//go:build !linux

package cli

import "io"

func runnerTerminal(io.Reader) bool { return false }

func readConnectionCode(input io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(input, 8193))
}
