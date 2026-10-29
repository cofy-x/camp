//go:build !linux

package documenttext

import "os/exec"

func readerCommand(Config, string) (*exec.Cmd, error) {
	return nil, failure("unavailable", "Document preparation requires Linux Bubblewrap. The original remains available to download.", true)
}
