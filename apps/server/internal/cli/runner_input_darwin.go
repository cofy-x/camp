//go:build darwin

package cli

import (
	"bufio"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func runnerTerminal(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return false
	}
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TIOCGETA)
	return err == nil
}

func readConnectionCode(input io.Reader) ([]byte, error) {
	if file, ok := input.(*os.File); ok && runnerTerminal(input) {
		fd := int(file.Fd())
		original, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
		if err != nil {
			return nil, err
		}
		updated := *original
		updated.Lflag &^= unix.ECHO
		if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &updated); err != nil {
			return nil, err
		}
		defer unix.IoctlSetTermios(fd, unix.TIOCSETA, original)
	}
	value, err := bufio.NewReader(io.LimitReader(input, 8193)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return []byte(value), nil
}
