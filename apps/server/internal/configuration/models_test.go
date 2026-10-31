package configuration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }
func TestPrecedencePrivateVersionsAndWriteOnlyKey(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "settings")
	models, err := Open(directory, Defaults(), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	view, err := models.Update(Update{APIKey: ptr("managed-secret"), Model: ptr("managed-model"), MaxSteps: ptr(3)})
	if err != nil {
		t.Fatal(err)
	}
	old := view.Revision
	if !view.APIKeyConfigured || view.Fields["apiKey"].Source != "managed" {
		t.Fatalf("view: %+v", view)
	}
	data, _ := json.Marshal(view)
	var public map[string]any
	_ = json.Unmarshal(data, &public)
	if strings.Contains(string(data), "managed-secret") || public["apiKey"] != nil {
		t.Fatal("key appeared in public view")
	}
	for _, file := range []string{filepath.Join(directory, "models.json"), filepath.Join(directory, "versions", old+".json")} {
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private permissions: %v %v", info, err)
		}
	}
	models, err = Open(directory, Defaults(), Overrides{EnvFile: map[string]string{"model": "file-model", "apiKey": "file-secret"}, Environment: map[string]string{"model": "process-model"}})
	if err != nil {
		t.Fatal(err)
	}
	view = models.View()
	if view.Model.Model != "process-model" || view.MaxSteps != 3 || view.Fields["apiKey"].Source != "env-file" || !view.Fields["model"].Overridden {
		t.Fatalf("precedence: %+v", view)
	}
	if _, err := models.Update(Update{Model: ptr("ignored")}); err == nil {
		t.Fatal("overridden field accepted")
	}
	frozen, err := models.Resolve(old)
	if err != nil || frozen.APIKey != "managed-secret" || frozen.Model != "managed-model" || frozen.MaxSteps != 3 {
		t.Fatal("queued configuration changed")
	}
	current, _ := models.Snapshot()
	if current == old {
		t.Fatal("effective configuration did not get new identity")
	}
	if text := models.Redact("managed-secret file-secret"); strings.Contains(text, "secret") {
		t.Fatal("private version key was not redacted")
	}
}
func TestSettingsFailureDoesNotChangeEffectiveVersion(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "settings")
	models, err := Open(directory, Defaults(), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	original, _ := models.Snapshot()
	if _, err := models.Update(Update{BaseURL: ptr("https://secret@provider.invalid")}); err == nil {
		t.Fatal("credential URL accepted")
	}
	if _, err := models.Update(Update{MaxTokens: ptr(99999)}); err == nil {
		t.Fatal("unbounded budget accepted")
	}
	if err := os.Remove(filepath.Join(directory, "models.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "models.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := models.Update(Update{APIKey: ptr("never-published")}); err == nil {
		t.Fatal("failed write accepted")
	}
	actual, model := models.Snapshot()
	if actual != original || model.APIKey != "" {
		t.Fatal("failed write changed effective configuration")
	}
}
func TestPrivateSettingsRejectUnsafePermissionsAndCorruption(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "settings")
	_, err := Open(directory, Defaults(), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "models.json")
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory, Defaults(), Overrides{}); err == nil {
		t.Fatal("public-readable secrets accepted")
	}
	if err := os.WriteFile(file, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory, Defaults(), Overrides{}); err == nil {
		t.Fatal("corruption silently reset")
	}
}

func TestPrivateSettingsRestrictExistingDirectories(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "settings")
	if err := os.MkdirAll(filepath.Join(directory, "versions"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory, Defaults(), Overrides{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{directory, filepath.Join(directory, "versions")} {
		info, err := os.Lstat(name)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatal("private settings directory is not restricted", err)
		}
	}
	linked := filepath.Join(t.TempDir(), "settings")
	if err := os.Symlink(directory, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(linked, Defaults(), Overrides{}); err == nil {
		t.Fatal("symlink settings directory accepted")
	}
}
func TestImportPolicyDefaultsAndSymlinkBoundary(t *testing.T) {
	if _, err := (ImportPolicy{}).Resolve("/tmp"); err == nil {
		t.Fatal("import enabled by default")
	}
	allowed, outside := t.TempDir(), t.TempDir()
	// macOS temporary paths may contain the system /var -> /private/var alias.
	allowed, err := filepath.EvalSymlinks(allowed)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewImportPolicy([]string{allowed})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := policy.Resolve(allowed); err != nil || got != allowed {
		t.Fatal("allowed root rejected")
	}
	if err := os.Symlink(outside, filepath.Join(allowed, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Resolve(filepath.Join(allowed, "escape")); err == nil {
		t.Fatal("symlink import escape accepted")
	}
	if _, err := policy.Resolve(allowed + "-sibling"); err == nil {
		t.Fatal("prefix sibling accepted")
	}
}
