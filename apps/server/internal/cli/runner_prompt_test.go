package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

type unexpectedInput struct{ t *testing.T }

func TestRepeatSetupRejectsChangedSourcesWithoutReplacingIdentity(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(p.Joined{RunnerID: "source-runner"})
	}))
	defer server.Close()
	directory := t.TempDir()
	source := t.TempDir()
	args := []string{"setup", "--executor", "fixture", "--server", server.URL, "--name", "Sources", "--data-dir", directory, "--source", "notes=" + source, "--code-file", "-"}
	input := runnerInput{reader: strings.NewReader("private-code\n"), isTerminal: func(io.Reader) bool { return false }, readSecret: readConnectionCode}
	if err := runRunnerWithInput(context.Background(), args, io.Discard, input); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(directory, "daemon.json"))
	if err != nil {
		t.Fatal(err)
	}
	input.reader = unexpectedInput{t}
	if err := runRunnerWithInput(context.Background(), args, io.Discard, input); err != nil {
		t.Fatal("Identical setup was not idempotent", err)
	}
	changed := append([]string{}, args...)
	for i, value := range changed {
		if value == "notes="+source {
			changed[i] = "notes=" + t.TempDir()
		}
	}
	if err := runRunnerWithInput(context.Background(), changed, io.Discard, input); err == nil || !strings.Contains(err.Error(), "--source is for initial setup") {
		t.Fatal("Conflicting source configuration was silently ignored", err)
	}
	after, err := os.ReadFile(filepath.Join(directory, "daemon.json"))
	if err != nil || !bytes.Equal(before, after) || requests != 1 {
		t.Fatal("Source retry changed identity/configuration or enrolled again", err, requests)
	}
}

func (r unexpectedInput) Read([]byte) (int, error) {
	r.t.Error("Setup attempted an unrequested read from stdin")
	return 0, io.EOF
}

func TestGuidedRunnerSetupAndSavedIdentity(t *testing.T) {
	joined := make(chan p.Enrollment, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/enroll" {
			t.Error("Unexpected setup request", r.URL.Path)
		}
		var enrollment p.Enrollment
		if err := json.NewDecoder(r.Body).Decode(&enrollment); err != nil {
			t.Error(err)
		}
		joined <- enrollment
		_ = json.NewEncoder(w).Encode(p.Joined{RunnerID: "guided-runner"})
	}))
	defer server.Close()
	directory := filepath.Join(t.TempDir(), "private storage")
	dockerSocket := filepath.Join(t.TempDir(), "custom Docker.sock")
	var output bytes.Buffer
	secretReads := 0
	input := runnerInput{
		reader:     strings.NewReader(directory + "\n" + server.URL + "\n" + "Studio Runner\n" + "private-connection-code\n"),
		isTerminal: func(io.Reader) bool { return true },
		readSecret: func(r io.Reader) ([]byte, error) { secretReads++; return readConnectionCode(r) },
	}
	if err := runRunnerWithInput(context.Background(), []string{"setup", "--executor", "fixture", "--docker-socket", dockerSocket}, &output, input); err != nil {
		t.Fatal(err)
	}
	enrollment := <-joined
	if enrollment.Name != "Studio Runner" || enrollment.Token != "private-connection-code" || secretReads != 1 {
		t.Fatal("Guided values or secret input were lost", enrollment.Name, secretReads)
	}
	for _, label := range []string{"Private Runner data directory", "Camp Server URL", "Machine name", "input is hidden", "Run in foreground", "Optional service", "Boot startup is a separate choice"} {
		if !strings.Contains(output.String(), label) {
			t.Error("Missing guided setup content", label)
		}
	}
	if strings.Contains(output.String(), enrollment.Token) {
		t.Fatal("Setup echoed the connection code")
	}
	config, err := daemon.LoadConfig(directory)
	if err != nil || config.RunnerID != "guided-runner" || config.PendingToken != "" {
		t.Fatal(config.RunnerID, err)
	}
	journal := filepath.Join(directory, "retained-journal")
	if err := os.WriteFile(journal, []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	input.reader = unexpectedInput{t}
	input.readSecret = func(io.Reader) ([]byte, error) {
		t.Error("Saved registration requested another code")
		return nil, io.EOF
	}
	output.Reset()
	if err := runRunnerWithInput(context.Background(), []string{"setup", "--executor", "fixture", "--data-dir", directory}, &output, input); err != nil {
		t.Fatal(err)
	}
	recovered, err := daemon.LoadConfig(directory)
	if err != nil || recovered.RunnerID != config.RunnerID || recovered.Credential != config.Credential || recovered.StorageID != config.StorageID {
		t.Fatal("Saved identity changed", err)
	}
	if recovered.DockerSocket != dockerSocket {
		t.Fatal("Repeated setup replaced the saved custom Docker socket")
	}
	correctedSocket := filepath.Join(t.TempDir(), "corrected.sock")
	if err := runRunnerWithInput(context.Background(), []string{"setup", "--executor", "fixture", "--data-dir", directory, "--docker-socket", correctedSocket}, io.Discard, input); err != nil {
		t.Fatal(err)
	}
	corrected, err := daemon.LoadConfig(directory)
	if err != nil || corrected.DockerSocket != correctedSocket || corrected.RunnerID != config.RunnerID {
		t.Fatal("Explicit Docker correction did not preserve registration", err)
	}
	if data, err := os.ReadFile(journal); err != nil || string(data) != "preserved" {
		t.Fatal("Setup replaced existing private data", err)
	}
	if !strings.Contains(output.String(), "Using saved connection") || len(joined) != 0 {
		t.Fatal("Saved registration was not reused")
	}
	for _, change := range []runnerSetupLocation{{directory: directory, server: "https://other.example"}, {directory: directory, name: "Different identity"}} {
		if _, err := promptRunnerSetup(unexpectedInput{t}, io.Discard, change); err == nil {
			t.Fatal("Guided setup accepted replacement identity")
		}
	}
}

func TestNoninteractiveSetupNeverPromptsForMissingValues(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "must-not-exist")
	for _, args := range [][]string{
		{"setup"},
		{"setup", "--server", "http://localhost", "--name", "Robot", "--data-dir", directory},
	} {
		input := runnerInput{reader: unexpectedInput{t}, isTerminal: func(io.Reader) bool { return false }, readSecret: readConnectionCode}
		if err := runRunnerWithInput(context.Background(), args, io.Discard, input); err == nil || !strings.Contains(err.Error(), "Noninteractive setup requires") {
			t.Fatal("Missing automation input was not rejected", err)
		}
		if _, err := os.Stat(directory); !os.IsNotExist(err) {
			t.Fatal("Incomplete automation setup wrote storage", err)
		}
	}
}

func TestExplicitConnectionCodeStdinRemainsNoninteractive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var enrollment p.Enrollment
		if err := json.NewDecoder(r.Body).Decode(&enrollment); err != nil {
			t.Error(err)
		}
		if enrollment.Token != "automation-code" {
			t.Error("Explicit stdin code was lost")
		}
		_ = json.NewEncoder(w).Encode(p.Joined{RunnerID: "automation-runner"})
	}))
	defer server.Close()
	var output bytes.Buffer
	input := runnerInput{reader: strings.NewReader("automation-code\n"), isTerminal: func(io.Reader) bool { return false }, readSecret: readConnectionCode}
	if err := runRunnerWithInput(context.Background(), []string{"setup", "--server", server.URL, "--name", "Robot", "--data-dir", t.TempDir(), "--code-file", "-", "--executor", "fixture"}, &output, input); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Enter the connection code") || strings.Contains(output.String(), "automation-code") {
		t.Fatal("Automation prompted or exposed the code")
	}
}

func TestPromptDefaultsAndEarlyEOF(t *testing.T) {
	value, err := runnerPrompt(strings.NewReader("\nremaining-secret\n"), io.Discard, "Machine name", "studio")
	if err != nil || value != "studio" {
		t.Fatal(value, err)
	}
	if _, err = runnerPrompt(strings.NewReader(""), io.Discard, "Camp Server URL", ""); err == nil {
		t.Fatal("Closed input did not end setup")
	}
	reader := strings.NewReader("value\nsecret-code\n")
	if _, err = runnerPrompt(reader, io.Discard, "Server", ""); err != nil {
		t.Fatal(err)
	}
	remaining, err := io.ReadAll(reader)
	if err != nil || string(remaining) != "secret-code\n" {
		t.Fatal("Prompt buffered the future secret", err)
	}
}

func TestSetupFollowupCommandsUseQuotedAbsoluteLauncher(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("Linux Runner instructions require a POSIX shell")
	}
	bundle := filepath.Join(t.TempDir(), "Runner's installation")
	directory := filepath.Join(t.TempDir(), "private storage's data")
	if err := os.MkdirAll(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(bundle, "camp")
	// The test launcher only reports arguments; it never runs a daemon or service.
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runnerNextSteps(&output, bundle, directory); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct{ label, arguments string }{
		{"Run in foreground: ", "runner\nrun\n--data-dir\n"},
		{"Optional service: ", "runner\nservice\ninstall\n--data-dir\n"},
		{"Start the installed service: ", "runner\nservice\nstart\n--data-dir\n"},
		{"Boot startup is a separate choice: ", "runner\nservice\nenable-boot\n--data-dir\n"},
	} {
		found := false
		for _, line := range strings.Split(output.String(), "\n") {
			if !strings.HasPrefix(line, step.label) {
				continue
			}
			found = true
			result, err := exec.Command(shell, "-c", strings.TrimPrefix(line, step.label)).CombinedOutput()
			if err != nil || string(result) != step.arguments+directory+"\n" {
				t.Fatalf("Copied command did not preserve its launcher or arguments: %q, %v", result, err)
			}
		}
		if !found {
			t.Fatal("Missing next step", step.label)
		}
	}
	output.Reset()
	if err := runnerNextSteps(&output, "", directory); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil || !strings.Contains(output.String(), runnerShellArgument(executable)+" runner run") {
		t.Fatal("Development instructions did not use the current executable", err)
	}
}
