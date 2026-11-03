//go:build darwin

package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type serviceObservationOutput struct{ bytes.Buffer }

func (b *serviceObservationOutput) Write(data []byte) (int, error) {
	n := len(data)
	if left := (64 << 10) - b.Len(); left > 0 {
		_, _ = b.Buffer.Write(data[:min(left, len(data))])
	}
	return n, nil
}

func serviceStatusLabel(role, directory string) string {
	digest := sha256.Sum256([]byte(directory))
	return fmt.Sprintf("dev.cofy.camp.%s.%x", role, digest[:8])
}

func readMacServiceStatus(ctx context.Context, directory, role string) serviceStatus {
	home, err := os.UserHomeDir()
	if err != nil {
		return serviceStatus{Supported: true, State: "unknown"}
	}
	read := func(target string) (string, error) {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(probe, "/bin/launchctl", "print", target)
		var output serviceObservationOutput
		cmd.Stdout, cmd.Stderr = &output, &output
		err := cmd.Run()
		return output.String(), err
	}
	return observeMacService(directory, role, filepath.Join(home, "Library", "LaunchAgents"), os.Geteuid(), read)
}

func observeMacService(directory, role, loginDirectory string, uid int, read func(string) (string, error)) serviceStatus {
	result := serviceStatus{Supported: true, State: "absent"}
	if role != "runner" && role != "server" {
		result.State = "unknown"
		return result
	}
	label := serviceStatusLabel(role, directory)
	check := func(name string) (bool, bool) {
		_, err := readManagedMacPlist(name)
		return err == nil, err != nil && !errors.Is(err, os.ErrNotExist)
	}
	invalid := false
	result.Installed, invalid = check(filepath.Join(directory, "service", label+".plist"))
	var badLogin bool
	result.LoginEnabled, badLogin = check(filepath.Join(loginDirectory, label+".plist"))
	invalid = invalid || badLogin
	loaded := 0
	for _, domain := range []string{"gui/" + strconv.Itoa(uid), "user/" + strconv.Itoa(uid)} {
		output, err := read(domain + "/" + label)
		if err != nil {
			// Both login domains can be absent on a headless account. Other failures
			// remain unknown instead of being interpreted as a stopped owned service.
			if !strings.Contains(output, "Could not find service") && !strings.Contains(output, "Could not find domain") {
				invalid = true
			}
			continue
		}
		loaded++
		for _, line := range strings.Split(output, "\n") {
			if strings.TrimSpace(line) == "state = running" {
				result.Running = true
			}
		}
	}
	result.Loaded = loaded > 0
	if invalid {
		result.State = "unknown"
		return result
	}
	if loaded > 1 {
		result.State = "ambiguous"
		return result
	}
	if result.Running {
		result.State = "running"
	} else if result.Installed || result.Loaded {
		result.State = "stopped"
	}
	return result
}
