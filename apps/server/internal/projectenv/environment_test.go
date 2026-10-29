package projectenv

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/resources"
)

func put(t *testing.T, directory, name, content string) {
	t.Helper()
	filename := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDetectAndValidateProfiles(t *testing.T) {
	project := t.TempDir()
	put(t, project, "package.json", `{"name":"example"}`)
	profile, err := Detect(project)
	if err != nil || profile.PackageManager != "node" || len(profile.Checks) != 1 {
		t.Fatalf("%+v %v", profile, err)
	}
	put(t, project, "package.json", `{"name":"@cofy-x/camp","packageManager":"pnpm@12.11.2","scripts":{"test":"node --test","typecheck":"tsc --noEmit","build":"vite build"}}`)
	put(t, project, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	profile, err = Detect(project)
	if err != nil || len(profile.Checks) != 3 {
		t.Fatalf("%+v %v", profile, err)
	}
	for _, mutate := range []func(*Profile){
		func(p *Profile) { p.PackageManager = "npm" },
		func(p *Profile) { p.Checks = []Check{{Name: "test", Directory: "../outside"}} },
		func(p *Profile) { p.Checks = []Check{{Name: "test", Script: "test; echo bad"}} },
		func(p *Profile) { p.Checks = []Check{{Name: "test"}, {Name: "test"}} },
	} {
		copied := profile
		mutate(&copied)
		if Validate(copied) == nil {
			t.Fatalf("Invalid profile was accepted: %+v", copied)
		}
	}
}

func TestManifestBoundaries(t *testing.T) {
	for _, fixture := range []struct{ name, content string }{
		{".pnpmfile.cjs", "throw new Error('must not run')"},
		{"pnpm-workspace.yaml", "packages: ['../outside']\n"},
		{"pnpm-workspace.yaml", "storeDir: /host\n"},
		{"package.json", `{"dependencies":{"outside":"file:../outside"}}`},
	} {
		directory := t.TempDir()
		put(t, directory, fixture.name, fixture.content)
		if validateManifests(directory) == nil {
			t.Fatalf("Unsafe input accepted: %s", fixture.name)
		}
	}
	directory := t.TempDir()
	put(t, directory, "package.json", `{"dependencies":{"inside":"file:packages/inside"}}`)
	put(t, directory, "pnpm-workspace.yaml", "packages: ['packages/*']\n")
	if err := validateManifests(directory); err != nil {
		t.Fatal(err)
	}
}

func TestCancelledPreparationDoesNotPublish(t *testing.T) {
	directory := t.TempDir()
	put(t, directory, "package.json", "{}")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Prepare(ctx, Options{ProjectDir: directory, CacheDir: filepath.Join(t.TempDir(), "cache")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}

func TestPinnedPnpmLifecycle(t *testing.T) {
	if os.Getenv("CAMP_PROJECT_ENV_TEST") != "1" {
		t.Skip("Set CAMP_PROJECT_ENV_TEST=1 for the real pnpm/Bubblewrap lifecycle check")
	}
	repository, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	pnpm, err := FindPnpm(filepath.Join(repository, "tools/dsh-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	put(t, project, "package.json", `{"name":"fixture","type":"module","packageManager":"pnpm@12.11.2","dependencies":{"inside":"workspace:*"},"scripts":{"postinstall":"node -e \"require('fs').writeFileSync('should-not-exist','bad')\"","test":"node --test","typecheck":"node check.mjs","build":"node build.mjs"}}`)
	put(t, project, "pnpm-workspace.yaml", "packages:\n  - packages/*\n")
	runtimeLock, err := os.ReadFile(filepath.Join(repository, "tools/project-runtime/pnpm-lock.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	managerLock := strings.Split(string(runtimeLock), "\n---\n")[0] + "\n---\n"
	put(t, project, "pnpm-lock.yaml", managerLock+"lockfileVersion: '9.0'\nsettings:\n  autoInstallPeers: true\n  excludeLinksFromLockfile: false\nimporters:\n  .:\n    dependencies:\n      inside:\n        specifier: workspace:*\n        version: link:packages/inside\n  packages/inside: {}\n")
	put(t, project, "packages/inside/package.json", `{"name":"inside","version":"1.0.0","type":"module","exports":"./index.js"}`)
	put(t, project, "packages/inside/index.js", "export const value = 42;\n")
	put(t, project, "source.test.mjs", "import assert from 'node:assert/strict'; import {value} from 'inside'; assert.equal(value,42); assert.equal(process.env.DEEPSEEK_API_KEY,undefined); assert.equal(process.version,'v24.16.0');\n")
	put(t, project, "check.mjs", "import fs from 'node:fs'; if(fs.existsSync('should-not-exist')) throw new Error('Install scripts ran'); fs.writeFileSync('check-output.txt','disposable');\n")
	put(t, project, "build.mjs", "import fs from 'node:fs'; fs.mkdirSync('dist',{recursive:true}); fs.writeFileSync('dist/index.html','<h1>Verified</h1>');\n")
	options := Options{NodePath: node, PnpmPath: pnpm, ProjectDir: project, CacheDir: filepath.Join(t.TempDir(), "cache"), Output: func(line string) error { t.Log(strings.TrimSpace(line)); return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	environment, err := Prepare(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(environment.Mounts) != 1 {
		t.Fatalf("%+v", environment.Mounts)
	}
	again, err := Prepare(ctx, options)
	if err != nil || again.Key != environment.Key {
		t.Fatalf("Cache replay: %+v %v", again, err)
	}
	transitions := []string{}
	verification, err := Verify(ctx, options, environment, func(result CheckResult) error {
		transitions = append(transitions, result.Name+":"+result.Status)
		return nil
	})
	defer os.RemoveAll(verification.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(verification.Checks) != 3 || len(transitions) != 6 {
		t.Fatalf("%+v %+v", verification, transitions)
	}
	for _, check := range verification.Checks {
		if check.Status != "passed" {
			t.Fatalf("%+v", check)
		}
	}
	if _, err := os.Stat(filepath.Join(verification.Directory, "project", "dist", "index.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, "check-output.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Verification modified the model workspace")
	}
	writeFailure := errors.New("check output storage is unavailable")
	failedOptions := options
	failedOptions.Output = func(line string) error {
		if strings.HasPrefix(line, "[test]") {
			return writeFailure
		}
		return nil
	}
	failedVerification, err := Verify(ctx, failedOptions, environment, nil)
	defer os.RemoveAll(failedVerification.Directory)
	if !errors.Is(err, writeFailure) || len(failedVerification.Checks) != 1 || failedVerification.Checks[0].Name != "test" {
		t.Fatalf("Output write failure must abort later checks: %+v %v", failedVerification.Checks, err)
	}
	// A model process cannot mutate the dependency cache or see server credentials.
	cmd, err := execution.ProjectCommand(execution.ProjectConfig{NodePath: node, PnpmPath: pnpm, ProjectDir: project, Dependencies: environment.Mounts, Arguments: []string{"/node", "-e", "const fs=require('fs'); try{fs.writeFileSync('node_modules/cache-write','bad');process.exit(2)}catch{}; if(process.env.DEEPSEEK_API_KEY)process.exit(3)"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, err := execution.RunTests(ctx, cmd, func(string) error { return nil })
	if err != nil || code != 0 {
		t.Fatalf("Boundary check: %d %v", code, err)
	}
	// Workspace source stays live; dependency manifest changes get a new preparation.
	put(t, project, "packages/inside/index.js", "export const value = 43;\n")
	changed, err := Prepare(ctx, options)
	if err != nil || changed.Key != environment.Key {
		t.Fatalf("Ordinary source edits unnecessarily rebuilt dependencies: %v", err)
	}
	put(t, project, "packages/inside/package.json", `{"name":"inside","version":"1.0.1","type":"module","exports":"./index.js"}`)
	changed, err = Prepare(ctx, options)
	if err != nil || changed.Key == environment.Key {
		t.Fatalf("Changed manifest reused old dependencies: %v", err)
	}
	put(t, project, "pnpm-lock.yaml", "invalid lockfile")
	if _, err := Prepare(ctx, options); err == nil {
		t.Fatal("Invalid frozen lockfile succeeded")
	}
	entries, err := os.ReadDir(options.CacheDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".prepare-") {
			t.Fatal("Failed preparation left temporary dependencies")
		}
	}
}

func TestActualCampEnvironment(t *testing.T) {
	if os.Getenv("CAMP_PROJECT_ENV_CAMP") != "1" {
		t.Skip("Set CAMP_PROJECT_ENV_CAMP=1 for actual Camp dependency installation and frontend checks")
	}
	repository, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(repository, ".data/project-env-camp-check")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(directory, "project")
	_ = os.RemoveAll(project)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if err := resources.CopySource(ctx, repository, project); err != nil {
		t.Fatal(err)
	}
	pnpm, err := FindPnpm(filepath.Join(repository, "tools/dsh-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	node, _ := exec.LookPath("node")
	options := Options{NodePath: node, PnpmPath: pnpm, ProjectDir: project, CacheDir: filepath.Join(directory, "cache"), Output: func(line string) error { t.Log(strings.TrimSpace(line)); return nil }}
	environment, err := Prepare(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	verification, err := Verify(ctx, options, environment, func(result CheckResult) error { t.Log(result.Name, result.Status); return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range verification.Checks {
		if check.Status != "passed" {
			t.Errorf("%s: %s", check.Name, check.Output)
		}
	}
	t.Log("Verification workspace", verification.Directory)
}

func TestCheckOutputBudgetIncludesMarkersAndJSONEscaping(t *testing.T) {
	output := boundedOutput(strings.Repeat("\x00", 100000))
	if len(output) > 16<<10 || !strings.HasPrefix(output, "[Earlier check output omitted]") {
		t.Fatal("Check output exceeds its bounded preview")
	}
	checks := make([]CheckResult, 8)
	for index := range checks {
		checks[index] = CheckResult{Name: "test", Command: "node --test", Status: "failed", Output: output}
	}
	encoded, err := json.Marshal(checks)
	if err != nil || len(encoded) > 1<<20 {
		t.Fatalf("Combined check output exceeds storage JSON budget: %d %v", len(encoded), err)
	}
}

func TestCopiedWorkspaceDependenciesIncludeSourceInCacheKey(t *testing.T) {
	fixtures := []struct{ name, manifest, workspace string }{
		{"workspace injection", `{"name":"fixture"}`, "packages: ['packages/*']\ninjectWorkspacePackages: true\n"},
		{"package injection", `{"name":"fixture","dependenciesMeta":{"inside":{"injected":true}}}`, "packages: ['packages/*']\n"},
		{"quoted patch setting", `{"name":"fixture"}`, "packages: ['packages/*']\n'patchedDependencies':\n  inside: patches/inside.patch\n"},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			directory := t.TempDir()
			put(t, directory, "package.json", fixture.manifest)
			put(t, directory, "pnpm-workspace.yaml", fixture.workspace)
			put(t, directory, "packages/inside/index.js", "export const value = 1;\n")
			before, err := fingerprint(context.Background(), directory)
			if err != nil {
				t.Fatal(err)
			}
			put(t, directory, "packages/inside/index.js", "export const value = 2;\n")
			after, err := fingerprint(context.Background(), directory)
			if err != nil {
				t.Fatal(err)
			}
			if before == after {
				t.Fatal("Copied workspace source changes reused stale dependency content")
			}
		})
	}
}
