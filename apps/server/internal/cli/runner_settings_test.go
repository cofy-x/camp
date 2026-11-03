package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestRepeatedSetupExplicitSourceUpdatesAndLocalViews(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		json.NewEncoder(w).Encode(p.Joined{RunnerID: "configured-runner"})
	}))
	defer server.Close()
	directory, source, other := t.TempDir(), t.TempDir(), t.TempDir()
	input := runnerInput{reader: strings.NewReader("private-code\n"), isTerminal: func(io.Reader) bool { return false }, readSecret: readConnectionCode}
	args := []string{"setup", "--executor", "fixture", "--server", server.URL, "--name", "Studio", "--data-dir", directory, "--source", "notes=" + source, "--code-file", "-"}
	if err := runRunnerWithInput(context.Background(), args, io.Discard, input); err != nil {
		t.Fatal(err)
	}
	original, err := daemon.LoadConfig(directory)
	if err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(directory, "journal-marker")
	if err = os.WriteFile(journal, []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	input.reader = unexpectedInput{t}
	repeat := func(extra ...string) error {
		return runRunnerWithInput(context.Background(), append([]string{"setup", "--data-dir", directory}, extra...), io.Discard, input)
	}
	if err = repeat(); err != nil {
		t.Fatal("Repeat setup did not reuse saved Server/name/executor", err)
	}
	if err = repeat("--replace-sources", "--source", "books="+other); err != nil {
		t.Fatal(err)
	}
	changed, err := daemon.LoadConfig(directory)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(other)
	if !reflect.DeepEqual(changed.Sources, map[string]string{"books": canonical}) {
		t.Fatal(changed.Sources)
	}
	expected := original
	expected.Sources = changed.Sources
	if !reflect.DeepEqual(changed, expected) {
		t.Fatal("Explicit source replacement changed other settings")
	}
	if err = repeat(); err != nil {
		t.Fatal(err)
	}
	retained, _ := daemon.LoadConfig(directory)
	if !reflect.DeepEqual(retained, changed) {
		t.Fatal("Omitted settings were not retained")
	}
	before, _ := os.ReadFile(filepath.Join(directory, "daemon.json"))
	for _, extra := range [][]string{{"--replace-sources"}, {"--clear-sources", "--source", "notes=" + source}, {"--clear-sources", "--replace-sources"}} {
		if err = repeat(extra...); err == nil {
			t.Fatal("Ambiguous source update accepted", extra)
		}
	}
	after, _ := os.ReadFile(filepath.Join(directory, "daemon.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("Invalid source flags changed configuration")
	}
	var output bytes.Buffer
	if err = runRunnerConfig([]string{"show", "--data-dir", directory, "--json"}, &output); err != nil {
		t.Fatal(err)
	}
	var visible map[string]any
	if err = json.Unmarshal(output.Bytes(), &visible); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{original.Credential, original.StorageID, original.RegistrationID, "credential", "pendingToken", "bundleDir", "dockerSocket"} {
		if private != "" && strings.Contains(output.String(), private) {
			t.Fatal("Local config leaked excluded field", private)
		}
	}
	if visible["sources"] == nil || visible["toolRoots"] == nil {
		t.Fatal("Local grants unavailable", output.String())
	}
	output.Reset()
	if err = runRunnerWithInput(context.Background(), []string{"status", "--data-dir", directory}, &output, input); err != nil || !strings.Contains(output.String(), "Configured workspace:") {
		t.Fatal("Human status", output.String(), err)
	}
	output.Reset()
	if err = runRunnerWithInput(context.Background(), []string{"status", "--data-dir", directory, "--json"}, &output, input); err != nil {
		t.Fatal(err)
	}
	var status runnerStatusReport
	if err = json.Unmarshal(output.Bytes(), &status); err != nil || status.Schema != 1 || status.Installed.State != "development" {
		t.Fatal("JSON status", output.String(), err)
	}
	after, _ = os.ReadFile(filepath.Join(directory, "daemon.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("Status/config inspection mutated configuration")
	}
	if err = repeat("--clear-sources"); err != nil {
		t.Fatal(err)
	}
	cleared, _ := daemon.LoadConfig(directory)
	if len(cleared.Sources) != 0 || cleared.RunnerID != original.RunnerID || cleared.Credential != original.Credential || requests != 1 {
		t.Fatal("Source clear enrolled again or changed identity", requests)
	}
	if bytes, err := os.ReadFile(journal); err != nil || string(bytes) != "preserved" {
		t.Fatal("Setup changed retained journal", err)
	}
}

func TestIncompletePinProvenanceRemainsUnknown(t *testing.T) {
	pin := runtimePin{ReleaseVersion: "v0.1.0-alpha.10", SHA256: strings.Repeat("b", 64)}
	if installationFromPin(pin) != nil {
		t.Fatal("Older pin fabricated a source commit")
	}
	pin.SourceCommit = strings.Repeat("a", 40)
	if value := installationFromPin(pin); value == nil || value.SourceCommit != pin.SourceCommit || value.RuntimeSHA256 != pin.SHA256 {
		t.Fatal("Complete verified provenance was lost", value)
	}
}
