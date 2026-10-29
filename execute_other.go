//go:build !linux && !darwin

package main

import "errors"

func execute(string, []string) error {
	return errors.New("Camp packages support Linux amd64 and macOS arm64/amd64; use a browser to access an existing Server on this platform")
}
