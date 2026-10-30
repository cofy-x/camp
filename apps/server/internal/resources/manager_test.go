//go:build linux || darwin

package resources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const testProjectID = "1234567890abcdef1234567890abcdef"

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	filename := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func nodeProject(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	writeFile(t, directory, "package.json", "{\"name\":\"camp-example\",\"type\":\"module\"}\n")
	writeFile(t, directory, "greet.js", "export const greet = () => 'Hello';\n")
	return directory
}

func readFile(t *testing.T, filename string) string {
	t.Helper()
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func sourceGit(t *testing.T, source string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", source}, args...)...)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_AUTHOR_NAME=Camp", "GIT_AUTHOR_EMAIL=camp@localhost", "GIT_COMMITTER_NAME=Camp", "GIT_COMMITTER_EMAIL=camp@localhost"}
	result, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Git test command failed: %v, %s", err, result)
	}
	return string(result)
}

func TestImportCaptureContinueAndExport(t *testing.T) {
	ctx := context.Background()
	source := nodeProject(t)
	sourceGit(t, source, "init", "--quiet")
	sourceGit(t, source, "add", ".")
	sourceGit(t, source, "commit", "--quiet", "-m", "Test baseline")
	dirty := "export const greet = () => 'Hello \u20ac';\n"
	writeFile(t, source, "greet.js", dirty)
	writeFile(t, source, ".env", "PRIVATE_TOKEN=must-not-enter-project\n")
	writeFile(t, source, "node_modules/ignored.dat", "\x00")
	status := sourceGit(t, source, "status", "--porcelain")
	manager := New(filepath.Join(t.TempDir(), "projects"))
	imported, err := manager.ImportWithPolicy(ctx, testProjectID, source, Policy{ExcludeNames: []string{"node_modules"}})
	if err != nil {
		t.Fatal(err)
	}
	canonicalSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	if imported.Files != 2 || imported.SourcePath != canonicalSource || !commitPattern.MatchString(imported.Commit) {
		t.Fatalf("Incorrect import result: %+v", imported)
	}
	run := t.TempDir()
	if err := manager.Prepare(ctx, testProjectID, imported.Commit, run); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(run, "project")
	if actual := readFile(t, filepath.Join(project, "greet.js")); actual != dirty {
		t.Fatalf("Existing changes were not preserved: %q", actual)
	}
	for _, name := range []string{".git", ".env", "node_modules"} {
		if _, err := os.Lstat(filepath.Join(project, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("The run directory exposed %s", name)
		}
	}
	changed := "export const greet = (name) => `Hello, ${name.trim()}`;\n"
	writeFile(t, project, "greet.js", changed)
	writeFile(t, project, "tests/greeting-\u20ac.test.js", "import assert from 'node:assert/strict';\nassert.ok(true);\n")
	firstCommit, files, err := manager.Capture(ctx, testProjectID, imported.Commit, run)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Status != "modified" || files[0].Before != dirty || files[0].After != changed || files[1].Status != "added" {
		t.Fatalf("Incorrect file differences: %+v", files)
	}
	secondRun := t.TempDir()
	if err := manager.Prepare(ctx, testProjectID, firstCommit, secondRun); err != nil {
		t.Fatal(err)
	}
	if actual := readFile(t, filepath.Join(secondRun, "project", "greet.js")); actual != changed {
		t.Fatal("The second run did not use the previous result")
	}
	if err := os.Remove(filepath.Join(secondRun, "project", "tests", "greeting-\u20ac.test.js")); err != nil {
		t.Fatal(err)
	}
	secondCommit, files, err := manager.Capture(ctx, testProjectID, firstCommit, secondRun)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Status != "deleted" {
		t.Fatalf("Incorrect deletion difference: %+v", files)
	}
	patch, err := manager.ExportPatch(ctx, testProjectID, imported.Commit, secondCommit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patch), changed[:20]) || strings.Contains(string(patch), "PRIVATE_TOKEN") {
		t.Fatalf("Incorrect patch content: %s", patch)
	}
	patchPath := filepath.Join(t.TempDir(), "changes.patch")
	if err := os.WriteFile(patchPath, patch, 0600); err != nil {
		t.Fatal(err)
	}
	sourceGit(t, source, "apply", "--check", patchPath)
	if actual := sourceGit(t, source, "status", "--porcelain"); actual != status {
		t.Fatalf("Import or export modified the source project: %q != %q", actual, status)
	}
	if readFile(t, filepath.Join(source, "greet.js")) != dirty {
		t.Fatal("The source project was modified")
	}
	if err := manager.Prepare(ctx, testProjectID, firstCommit, secondRun); err == nil {
		t.Fatal("An existing run directory must not be overwritten")
	}
}

func TestImportRefusesUnsafeOrUnsupportedFiles(t *testing.T) {
	cases := []struct {
		name   string
		modify func(*testing.T, string)
	}{
		{"symbolic link", func(t *testing.T, source string) {
			if err := os.Symlink("/etc/passwd", filepath.Join(source, "linked")); err != nil {
				t.Fatal(err)
			}
		}},
		{"hard link", func(t *testing.T, source string) {
			if err := os.Link(filepath.Join(source, "greet.js"), filepath.Join(source, "linked")); err != nil {
				t.Fatal(err)
			}
		}},
		{"named pipe", func(t *testing.T, source string) {
			if err := syscall.Mkfifo(filepath.Join(source, "pipe"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"oversized file", func(t *testing.T, source string) {
			writeFile(t, source, "large.txt", strings.Repeat("a", MaxFileBytes+1))
		}},
		{"control character in path", func(t *testing.T, source string) { writeFile(t, source, "bad\nname", "content") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			source := nodeProject(t)
			test.modify(t, source)
			manager := New(filepath.Join(t.TempDir(), "projects"))
			if _, err := manager.Import(context.Background(), testProjectID, source); err == nil {
				t.Fatal("Unsafe projects must be rejected")
			}
			if _, err := os.Lstat(filepath.Join(manager.root, testProjectID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("A failed import must not publish a repository")
			}
		})
	}
}

func TestCapturePreservesReadableChangesOnUnsafeResult(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{".env", ".git/config", "keys/private.pem"} {
		t.Run(name, func(t *testing.T) {
			manager := New(filepath.Join(t.TempDir(), "projects"))
			imported, err := manager.Import(ctx, testProjectID, nodeProject(t))
			if err != nil {
				t.Fatal(err)
			}
			run := t.TempDir()
			if err := manager.Prepare(ctx, testProjectID, imported.Commit, run); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(run, "project"), "greet.js", "export const greet = () => 'updated';\n")
			writeFile(t, filepath.Join(run, "project"), name, "sensitive content")
			commit, files, err := manager.Capture(ctx, testProjectID, imported.Commit, run)
			if err == nil || commit != "" || len(files) != 1 || files[0].Path != "greet.js" || !strings.Contains(files[0].After, "updated") {
				t.Fatalf("Unsafe results must retain readable differences: %q %+v %v", commit, files, err)
			}
			if strings.Contains(err.Error(), "sensitive content") {
				t.Fatal("Errors must not include file contents")
			}
		})
	}
}

func TestRejectsForeignCommitsAndPathEscape(t *testing.T) {
	ctx := context.Background()
	manager := New(filepath.Join(t.TempDir(), "projects"))
	imported, err := manager.Import(ctx, testProjectID, nodeProject(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Prepare(ctx, "../escape", imported.Commit, t.TempDir()); err == nil {
		t.Fatal("Project IDs must not escape the storage root")
	}
	if err := manager.Prepare(ctx, testProjectID, "HEAD", t.TempDir()); err == nil {
		t.Fatal("A revision must identify an explicitly saved commit")
	}
	if err := manager.Prepare(ctx, strings.Repeat("a", 32), imported.Commit, t.TempDir()); err == nil {
		t.Fatal("A revision from another project must not be readable")
	}
	if _, err := manager.ExportPatch(ctx, testProjectID, imported.Commit, "--output=/tmp/unsafe"); err == nil {
		t.Fatal("A revision must not become a Git option")
	}
	linked := filepath.Join(t.TempDir(), "source")
	if err := os.Symlink(nodeProject(t), linked); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Import(ctx, strings.Repeat("b", 32), linked); err == nil {
		t.Fatal("The source root must not be a symbolic link")
	}
}

func TestCaptureLimitsEncodedDifferences(t *testing.T) {
	ctx := context.Background()
	source := nodeProject(t)
	for _, name := range []string{"a", "b", "c", "d"} {
		writeFile(t, source, name, strings.Repeat("\t", 100000))
	}
	manager := New(filepath.Join(t.TempDir(), "projects"))
	imported, err := manager.Import(ctx, testProjectID, source)
	if err != nil {
		t.Fatal(err)
	}
	run := t.TempDir()
	if err := manager.Prepare(ctx, testProjectID, imported.Commit, run); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c", "d"} {
		writeFile(t, filepath.Join(run, "project"), name, strings.Repeat("\n", 100000))
	}
	commit, files, err := manager.Capture(ctx, testProjectID, imported.Commit, run)
	if err != nil || commit == "" || len(files) != 4 || !strings.Contains(files[3].After, "Preview omitted") {
		t.Fatalf("Retain complete versions with explicitly bounded previews: %s, %d, %v", commit, len(files), err)
	}
}

func TestCancelledImportDoesNotPublish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager := New(filepath.Join(t.TempDir(), "projects"))
	if _, err := manager.Import(ctx, testProjectID, nodeProject(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Expected cancellation: %v", err)
	}
	entries, err := os.ReadDir(manager.root)
	if (err != nil && !errors.Is(err, os.ErrNotExist)) || len(entries) != 0 {
		t.Fatalf("A cancelled import left a repository: %v %v", entries, err)
	}
}

func TestGitIgnoresCallerConfiguration(t *testing.T) {
	ctx := context.Background()
	source := nodeProject(t)
	writeFile(t, source, ".gitattributes", "*.js filter=private diff=private\n")
	config := filepath.Join(t.TempDir(), "config")
	writeFile(t, filepath.Dir(config), filepath.Base(config), "[filter \"private\"]\nclean = false\nrequired = true\n[diff \"private\"]\ncommand = false\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "filter.private.required")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	manager := New(filepath.Join(t.TempDir(), "projects"))
	imported, err := manager.Import(ctx, testProjectID, source)
	if err != nil {
		t.Fatal(err)
	}
	run := t.TempDir()
	if err := manager.Prepare(ctx, testProjectID, imported.Commit, run); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(run, "project"), "greet.js", "export const greet = () => 'safe';\n")
	commit, _, err := manager.Capture(ctx, testProjectID, imported.Commit, run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ExportPatch(ctx, testProjectID, imported.Commit, commit); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsSourcesInsideManagedDataOrContainingRepository(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	manager := New(filepath.Join(data, "projects"))
	for _, source := range []string{data, filepath.Join(data, "tasks", "run", "project"), manager.root} {
		writeFile(t, source, "package.json", "{}")
		if _, err := manager.Import(ctx, testProjectID, source); err == nil {
			t.Fatalf("Managed data must not be imported: %s", source)
		}
	}
	outer := nodeProject(t)
	inside := New(filepath.Join(outer, "camp-data", "projects"))
	if _, err := inside.Import(ctx, testProjectID, outer); err == nil {
		t.Fatal("A source containing the Camp revision store must not be imported recursively")
	}
}

func TestDiscardOnlyUnregisteredProject(t *testing.T) {
	ctx := context.Background()
	source := nodeProject(t)
	manager := New(filepath.Join(t.TempDir(), "projects"))
	if _, err := manager.Import(ctx, testProjectID, source); err != nil {
		t.Fatal(err)
	}
	if err := manager.DiscardImported("../outside"); err == nil {
		t.Fatal("Cleanup paths must not escape the storage root")
	}
	if err := manager.DiscardImported(testProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(manager.root, testProjectID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("The unregistered repository was not removed")
	}
	if err := manager.DiscardImported(testProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Import(ctx, testProjectID, source); err != nil {
		t.Fatalf("Import must succeed after cleanup: %v", err)
	}
	if readFile(t, filepath.Join(source, "greet.js")) == "" {
		t.Fatal("Source files must not be deleted")
	}
}

func TestImportLimitsFileCountAndTotalBytes(t *testing.T) {
	t.Run("file count", func(t *testing.T) {
		source := nodeProject(t)
		for count := 0; count < MaxFiles; count++ {
			writeFile(t, source, fmt.Sprintf("file-%05d.txt", count), "a")
		}
		manager := New(filepath.Join(t.TempDir(), "projects"))
		if _, err := manager.Import(context.Background(), testProjectID, source); err == nil {
			t.Fatal("Projects exceeding the file count limit must be rejected")
		}
	})
	t.Run("total bytes", func(t *testing.T) {
		source := nodeProject(t)
		for index := 0; index < MaxProjectBytes/MaxFileBytes+1; index++ {
			writeFile(t, source, fmt.Sprintf("large-%d", index), strings.Repeat("a", MaxFileBytes))
		}
		manager := New(filepath.Join(t.TempDir(), "projects"))
		if _, err := manager.Import(context.Background(), testProjectID, source); err == nil {
			t.Fatal("Projects exceeding the total size limit must be rejected")
		}
	})
}

func TestBinaryAssetsAndIgnoredBuildFiles(t *testing.T) {
	ctx := context.Background()
	source := nodeProject(t)
	putAsset := func(directory string) { writeFile(t, directory, "public/icon.png", "\x89PNG\x00\xff") }
	putAsset(source)
	writeFile(t, source, ".gitignore", "scratch/\n*.generated\ndist/\n.data/\nnode_modules/\n")
	writeFile(t, source, "scratch/unrelated.bin", "\x00")
	writeFile(t, source, "ignored.generated", "ignored")
	writeFile(t, source, "dist/index.html", "build output")
	writeFile(t, source, ".data/database.sqlite", "private runtime")
	manager := New(filepath.Join(t.TempDir(), "projects"))
	imported, err := manager.Import(ctx, testProjectID, source)
	if err != nil {
		t.Fatal(err)
	}
	run := t.TempDir()
	if err := manager.Prepare(ctx, testProjectID, imported.Commit, run); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(run, "project")
	for _, name := range []string{"scratch", "ignored.generated", "dist", ".data"} {
		if _, err := os.Stat(filepath.Join(project, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Ignored path exposed: %s", name)
		}
	}
	if readFile(t, filepath.Join(project, "public/icon.png")) != "\x89PNG\x00\xff" {
		t.Fatal("Binary asset changed")
	}
	writeFile(t, project, "public/icon.png", "\x89PNG\x00\xff\x01")
	writeFile(t, project, "node_modules/generated/package.json", "{}")
	writeFile(t, project, "dist/index.html", "check output")
	commit, files, err := manager.Capture(ctx, testProjectID, imported.Commit, run)
	if err != nil || commit == "" || len(files) != 1 || !strings.Contains(files[0].After, "Binary file") {
		t.Fatalf("%s %+v %v", commit, files, err)
	}
	patch, err := manager.ExportPatch(ctx, testProjectID, imported.Commit, commit)
	if err != nil || !strings.Contains(string(patch), "GIT binary patch") {
		t.Fatalf("Binary patch was not retained: %v", err)
	}
}

func TestNewIgnoreRuleDoesNotDeleteSavedSource(t *testing.T) {
	ctx := context.Background()
	manager := New(filepath.Join(t.TempDir(), "projects"))
	imported, err := manager.Import(ctx, testProjectID, nodeProject(t))
	if err != nil {
		t.Fatal(err)
	}
	run := t.TempDir()
	if err := manager.Prepare(ctx, testProjectID, imported.Commit, run); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(run, "project"), ".gitignore", "greet.js\n")
	commit, files, err := manager.Capture(ctx, testProjectID, imported.Commit, run)
	if err != nil || commit == "" || len(files) != 1 || files[0].Path != ".gitignore" {
		t.Fatalf("New ignores removed saved source: %+v %v", files, err)
	}
}
