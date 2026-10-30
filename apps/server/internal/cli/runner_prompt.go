package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
)

type runnerInput struct {
	reader     io.Reader
	isTerminal func(io.Reader) bool
	readSecret func(io.Reader) ([]byte, error)
}

type runnerSetupLocation struct{ directory, server, name string }

func runnerShellArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func runnerNextSteps(output io.Writer, bundle, directory string) error {
	var executable string
	var err error
	if bundle != "" {
		// The release launcher supplies the pinned Node and runtime environment.
		// The embedded binary is not a substitute, and neither is a PATH lookup.
		executable = filepath.Join(bundle, "camp")
	} else {
		executable, err = os.Executable()
		if err != nil {
			return err
		}
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	command, data := runnerShellArgument(executable), runnerShellArgument(directory)
	startup, label := "enable-boot", "Boot startup"
	if runtime.GOOS == "darwin" {
		startup, label = "enable-login", "Login startup"
	}
	_, err = fmt.Fprintf(output, "Setup complete.\nRun in foreground: %s runner run --data-dir %s\nOptional service: %s runner service install --data-dir %s\nStart the installed service: %s runner service start --data-dir %s\n%s is a separate choice: %s runner service %s --data-dir %s\n", command, data, command, data, command, data, label, command, startup, data)
	return err
}

func promptRunnerSetup(input io.Reader, output io.Writer, location runnerSetupLocation) (runnerSetupLocation, error) {
	var err error
	// Choose storage first so a repeated setup can recover the exact registration
	// instead of prompting for a replacement code or another Server's identity.
	if location.directory == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return location, err
		}
		location.directory, err = runnerPrompt(input, output, "Private Runner data directory", filepath.Join(base, "camp", "runner"))
		if err != nil {
			return location, err
		}
	}
	location.directory, err = filepath.Abs(location.directory)
	if err != nil {
		return location, err
	}
	previous, loadErr := daemon.LoadConfig(location.directory)
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		return location, loadErr
	}
	if loadErr == nil && (previous.RunnerID != "" || previous.PendingToken != "") {
		if location.server != "" && strings.TrimRight(location.server, "/") != previous.ServerURL {
			return location, errors.New("This private directory belongs to another Camp Server; choose its saved connection or a different data directory")
		}
		if location.name != "" && location.name != previous.Name {
			return location, errors.New("This private directory already has a Runner identity; rename the Runner in Camp instead of replacing its setup name")
		}
		if location.server == "" {
			location.server = previous.ServerURL
		}
		if location.name == "" {
			location.name = previous.Name
		}
		fmt.Fprintln(output, "Using saved connection for", previous.Name, "at", previous.ServerURL)
	}
	if location.server == "" {
		location.server, err = runnerPrompt(input, output, "Camp Server URL", "")
		if err != nil {
			return location, err
		}
	}
	if location.name == "" {
		name, _ := os.Hostname()
		if name == "" {
			name = runtime.GOOS + "-runner"
		}
		location.name, err = runnerPrompt(input, output, "Machine name", name)
		if err != nil {
			return location, err
		}
	}
	return location, nil
}

func runnerPrompt(input io.Reader, output io.Writer, label, fallback string) (string, error) {
	for {
		if fallback == "" {
			fmt.Fprintf(output, "%s: ", label)
		} else {
			fmt.Fprintf(output, "%s [%s]: ", label, fallback)
		}
		// Do not buffer ahead: the next line can be the secret connection code,
		// which must be read only after terminal echo has been disabled.
		var value []byte
		for {
			var one [1]byte
			_, err := io.ReadFull(input, one[:])
			if err != nil {
				if errors.Is(err, io.EOF) && len(value) > 0 {
					break
				}
				return "", fmt.Errorf("Setup input ended while reading %s; rerun setup or provide explicit flags", label)
			}
			if one[0] == '\n' {
				break
			}
			value = append(value, one[0])
			if len(value) > 4096 {
				return "", fmt.Errorf("%s is too long", label)
			}
		}
		answer := strings.TrimSpace(string(value))
		if answer == "" {
			answer = fallback
		}
		if answer != "" {
			return answer, nil
		}
		fmt.Fprintln(output, "Enter a value to continue.")
	}
}
