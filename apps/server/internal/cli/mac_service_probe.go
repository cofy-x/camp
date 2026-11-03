package cli

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

var errMacServiceNotFound = errors.New("The owned service or login domain is not loaded")

// Only a normally exited launchctl with its controlled absence diagnostic can
// establish not-found. Timeout, cancellation, permissions and failed spawns are
// uncertain observations, never permission to replace a process or its files.
func readMacServiceTarget(ctx context.Context, target string) (string, error) {
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probe, "/bin/launchctl", "print", target)
	var output serviceObservationOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	if probe.Err() != nil {
		return output.String(), probe.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ProcessState != nil && exit.ProcessState.Exited() && (strings.Contains(output.String(), "Could not find service") || strings.Contains(output.String(), "Could not find domain")) {
			return output.String(), errMacServiceNotFound
		}
	}
	return output.String(), err
}

func lookupMacService(ctx context.Context, target string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, err := readMacServiceTarget(ctx, target)
	if failure := ctx.Err(); failure != nil {
		return false, failure
	}
	if errors.Is(err, errMacServiceNotFound) {
		return false, nil
	}
	if err != nil {
		return false, errors.Join(errors.New("The owned service state could not be confirmed"), err)
	}
	return true, nil
}
