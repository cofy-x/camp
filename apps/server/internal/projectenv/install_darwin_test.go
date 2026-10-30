//go:build darwin

package projectenv

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nativeInstallOptions(t *testing.T) Options {
	t.Helper()
	if os.Getenv("CAMP_TEST_MAC_PACKAGE") != "1" {
		t.Skip("requires actual native Mac package qualification")
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
	node, err = filepath.EvalSymlinks(node)
	if err != nil {
		t.Fatal(err)
	}
	helper := os.Getenv("CAMP_MAC_HELPER")
	if helper == "" {
		helper = filepath.Join(t.TempDir(), "camp-sandbox")
		if output, err := exec.Command("go", "build", "-o", helper, "../../cmd/camp-sandbox").CombinedOutput(); err != nil {
			t.Fatalf("native helper: %v %s", err, output)
		}
	}
	project := filepath.Join(t.TempDir(), "project")
	put(t, project, "package.json", `{"name":"native-install-proof","packageManager":"pnpm@12.11.2","dependencies":{"inside":"workspace:*","is-number":"7.0.0"},"scripts":{"postinstall":"node -e \"require('fs').writeFileSync('install-script-ran','unsafe')\""}}`)
	put(t, project, "pnpm-workspace.yaml", "packages:\n  - packages/*\n")
	put(t, project, "packages/inside/package.json", `{"name":"inside","version":"1.0.0","main":"index.js"}`)
	put(t, project, "packages/inside/index.js", "module.exports=42;\n")
	runtimeLock, err := os.ReadFile(filepath.Join(repository, "tools/project-runtime/pnpm-lock.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	managerLock := strings.Split(string(runtimeLock), "\n---\n")[0] + "\n---\n"
	put(t, project, "pnpm-lock.yaml", managerLock+`lockfileVersion: '9.0'
settings:
  autoInstallPeers: true
  excludeLinksFromLockfile: false
importers:
  .:
    dependencies:
      inside:
        specifier: workspace:*
        version: link:packages/inside
      is-number:
        specifier: 7.0.0
        version: 7.0.0
  packages/inside: {}
packages:
  is-number@7.0.0:
    resolution: {integrity: sha512-41Cifkg6e8TylSpdtTpeLVMqvSBEVzTttHvERD741+pnZ8ANv0004MRL43QKPDlK9cGvNp6NZWZUBlbGXYxxng==}
    engines: {node: '>=0.12.0'}
snapshots:
  is-number@7.0.0: {}
`)
	return Options{NodePath: node, PnpmPath: pnpm, HelperPath: helper, JobRoot: filepath.Join(t.TempDir(), "native-dependency-jobs"), ProjectDir: project, CacheDir: filepath.Join(t.TempDir(), "cache"), Output: func(line string) error { t.Log(strings.TrimSpace(line)); return nil }}
}

func TestNativeLockedPnpmInstallAndCacheWithoutAmbientConfiguration(t *testing.T) {
	options := nativeInstallOptions(t)
	host := t.TempDir()
	put(t, host, ".npmrc", "registry=http://127.0.0.1:1/\n")
	t.Setenv("HOME", host)
	t.Setenv("npm_config_registry", "http://127.0.0.1:1/")
	t.Setenv("DEEPSEEK_API_KEY", "host-only-model-key")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	environment, err := Prepare(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(environment.Mounts) != 1 {
		t.Fatal("prepared dependencies were not returned", environment.Mounts)
	}
	target := filepath.Join(options.CacheDir, environment.Key, "project")
	for _, name := range []string{"is-number", "inside"} {
		if _, err := os.Stat(filepath.Join(target, "node_modules", name, "index.js")); err != nil {
			t.Fatal("locked package is missing", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "install-script-ran")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("package install script executed", err)
	}
	if _, err := os.Stat(filepath.Join(options.ProjectDir, "install-script-ran")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("original source changed", err)
	}
	again, err := Prepare(ctx, options)
	if err != nil || again.Key != environment.Key {
		t.Fatal("native prepared cache was not reused", err)
	}
}

func TestNativeUnknownPreparationRetainsStagingAndNeverPublishesReady(t *testing.T) {
	options := nativeInstallOptions(t)
	helper := filepath.Join(t.TempDir(), "unconfirmed-owner")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	options.HelperPath = helper
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := Prepare(ctx, options)
	if !CleanupUnconfirmed(err) {
		t.Fatal("missing native cleanup receipt was accepted", err)
	}
	entries, err := os.ReadDir(options.CacheDir)
	if err != nil {
		t.Fatal(err)
	}
	retained := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".prepare-") {
			retained = true
		}
		if _, err := os.Stat(filepath.Join(options.CacheDir, entry.Name(), "ready")); err == nil {
			t.Fatal("unknown preparation published a ready cache")
		}
	}
	if !retained {
		t.Fatal("unknown preparation deleted its staging data")
	}
}
