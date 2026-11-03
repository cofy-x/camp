package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

type installedRunnerStatus struct {
	State        string          `json:"state"`
	Installation *p.Installation `json:"installation,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	VerifiedAt   string          `json:"verifiedAt,omitempty"`
}

type runnerStatusReport struct {
	daemon.StatusReport
	Installed installedRunnerStatus `json:"installed"`
	Service   serviceStatus         `json:"service"`
}

func installationFromPin(pin runtimePin) *p.Installation {
	value := &p.Installation{Version: pin.ReleaseVersion, SourceCommit: pin.SourceCommit, RuntimeSHA256: pin.SHA256}
	if p.ValidateInstallation(value) != nil {
		return nil
	}
	return value
}

func runRunnerStatus(ctx context.Context, directory string, output io.Writer, jsonOutput bool) error {
	value, err := daemon.ReadStatus(directory)
	if err != nil {
		return err
	}
	report := runnerStatusReport{StatusReport: value, Installed: readInstalledRunnerStatus(ctx, directory), Service: readMacServiceStatus(ctx, directory, "runner")}
	if report.Installed.State == "invalid" || report.Installed.State == "unavailable" {
		report.ReadinessState = "historical"
	}
	if jsonOutput {
		return json.NewEncoder(output).Encode(report)
	}
	destination := output
	var formatted bytes.Buffer
	output = &formatted
	if _, err = fmt.Fprintf(output, "Runner: %s\nServer: %s\nConnection: %s\nConfigured workspace: %s\nInstalled runtime: %s\n", report.Name, report.Server, report.Connection, report.Configured.WorkspaceBackend, report.Installed.State); err != nil {
		return err
	}
	if report.Installed.VerifiedAt != "" {
		fmt.Fprintf(output, "Package verified: %s (recorded; status does not recheck every runtime file)\n", report.Installed.VerifiedAt)
	}
	if installed := report.Installed.Installation; installed != nil {
		fmt.Fprintf(output, "Package: %s · source %s\n", installed.Version, installed.SourceCommit)
	}
	if report.Installed.Reason != "" {
		fmt.Fprintln(output, report.Installed.Reason)
	}
	if report.Observed != nil {
		facts := report.Observed.Capabilities
		fmt.Fprintf(output, "Observed execution: %s · %s · %s · %s\n", report.Observed.State, facts.Workspace, facts.Sandbox, facts.Checkpoint)
	}
	if report.Readiness == nil {
		fmt.Fprintln(output, "Execution check: not observed")
	} else {
		state := "needs attention"
		if report.Readiness.Ready {
			state = "passed"
		}
		fmt.Fprintf(output, "Execution check: %s · %s · %s\n", state, report.ReadinessState, report.Readiness.CheckedAt)
		for _, check := range report.Readiness.Checks {
			if check.Status != "passed" {
				fmt.Fprintf(output, "  %s: %s\n", check.Label, check.Detail)
			}
		}
	}
	if report.Reason != "" {
		fmt.Fprintln(output, report.Reason)
	}
	if report.Service.Supported {
		fmt.Fprintf(output, "Service: %s · login startup %t\n", report.Service.State, report.Service.LoginEnabled)
	}
	_, err = io.Copy(destination, &formatted)
	return err
}

func runnerConfigDirectory(directory string) (string, error) {
	if directory == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		directory = filepath.Join(base, "camp", "runner")
	}
	return filepath.Abs(directory)
}

func runRunnerConfig(args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "show" {
		return errors.New("Usage: camp runner config show --data-dir PATH [--json]")
	}
	flags := flag.NewFlagSet("runner config show", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var directory string
	var jsonOutput bool
	flags.StringVar(&directory, "data-dir", "", "private Runner directory")
	flags.BoolVar(&jsonOutput, "json", false, "machine-readable local configuration")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("Runner config does not accept positional arguments")
	}
	directory, err := runnerConfigDirectory(directory)
	if err != nil {
		return err
	}
	config, err := daemon.LoadConfig(directory)
	if err != nil {
		return err
	}
	// This is an explicit local-operator view. Never marshal Config itself: its
	// credentials, pending enrollment token and private installation internals
	// must stay out of machine-readable output.
	value := struct {
		Schema           int               `json:"schema"`
		RunnerID         string            `json:"runnerId"`
		Name             string            `json:"name"`
		WorkspaceBackend string            `json:"workspaceBackend"`
		Sources          map[string]string `json:"sources"`
		ToolRoots        []string          `json:"toolRoots"`
	}{1, config.RunnerID, config.Name, config.WorkspaceBackend, config.Sources, config.ToolRoots}
	if value.WorkspaceBackend == "" {
		status, e := daemon.ReadStatus(directory)
		if e != nil {
			return e
		}
		value.WorkspaceBackend = status.Configured.WorkspaceBackend
	}
	if value.Sources == nil {
		value.Sources = map[string]string{}
	}
	if value.ToolRoots == nil {
		value.ToolRoots = []string{}
	}
	if jsonOutput {
		return json.NewEncoder(output).Encode(value)
	}
	destination := output
	var formatted bytes.Buffer
	output = &formatted
	fmt.Fprintf(output, "Runner: %s\nWorkspace: %s\n", value.Name, value.WorkspaceBackend)
	aliases := make([]string, 0, len(value.Sources))
	for alias := range value.Sources {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	fmt.Fprintln(output, "Source aliases:")
	for _, alias := range aliases {
		fmt.Fprintf(output, "  %s = %s\n", alias, value.Sources[alias])
	}
	fmt.Fprintln(output, "Tool grants:")
	for _, root := range value.ToolRoots {
		fmt.Fprintln(output, " ", root)
	}
	_, err = io.Copy(destination, &formatted)
	return err
}
