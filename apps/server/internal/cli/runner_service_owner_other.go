//go:build !linux && !darwin

package cli

import "errors"

func serviceDataOwner(string) (uint32, error) {
	return 0, errors.New("Runner services require Linux systemd")
}
