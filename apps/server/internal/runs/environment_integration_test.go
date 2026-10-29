package runs

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestDSHIntegrationPreparedPnpmDependencies(t *testing.T) {
	config := nativeConfig(t)
	var calls atomic.Int32
	m, s, _, room := integrationManager(t, config, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			integrationMessage(t, w, map[string]any{"type": "tool_use", "id": "use-dependency", "name": "bash", "input": map[string]string{"description": "Use the prepared workspace dependency", "command": "pnpm --version; pnpm run test; printf 'Verified prepared dependency.\\n' > result.md"}}, "tool_use")
			return
		}
		integrationMessage(t, w, map[string]any{"type": "text", "text": "Prepared pnpm dependency used in the isolated workspace."}, "end_turn")
	})
	source := t.TempDir()
	lock, err := os.ReadFile(filepath.Join(filepath.Dir(config.RuntimeDir), "project-runtime", "pnpm-lock.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	managerLock := strings.Split(string(lock), "\n---\n")[0] + "\n---\n"
	files := map[string]string{
		"package.json":                 `{"name":"fixture","type":"module","packageManager":"pnpm@12.11.2","dependencies":{"inside":"workspace:*"},"scripts":{"postinstall":"node -e \"require('fs').writeFileSync('should-not-exist','bad')\"","test":"node --test"}}`,
		"pnpm-workspace.yaml":          "packages:\n  - packages/*\n",
		"pnpm-lock.yaml":               managerLock + "lockfileVersion: '9.0'\nsettings:\n  autoInstallPeers: true\n  excludeLinksFromLockfile: false\nimporters:\n  .:\n    dependencies:\n      inside:\n        specifier: workspace:*\n        version: link:packages/inside\n  packages/inside: {}\n",
		"packages/inside/package.json": `{"name":"inside","version":"1.0.0","type":"module","exports":"./index.js"}`,
		"packages/inside/index.js":     "export const value = 42;\n",
		"source.test.mjs":              "import assert from 'node:assert/strict'; import fs from 'node:fs'; import {value} from 'inside'; assert.equal(value,42); assert.equal(fs.existsSync('should-not-exist'),false); assert.equal(process.env.DEEPSEEK_API_KEY,undefined); assert.equal(process.version,'v24.16.0');\n",
	}
	for name, content := range files {
		target := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	profile, err := projectenv.Detect(source)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("d", 32)
	imported, err := m.resources.ImportWithPolicy(context.Background(), id, source, projectenv.SnapshotPolicy(profile))
	if err != nil {
		t.Fatal(err)
	}
	environment, _ := json.Marshal(profile)
	if _, err := s.CreateResource(context.Background(), storage.Resource{ID: id, Name: "Dependency fixture", CurrentCommit: imported.Commit, Environment: environment}); err != nil {
		t.Fatal(err)
	}
	room.ResourceID = id
	room, err = s.UpdateRoom(context.Background(), room)
	if err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, m, sendTo(t, m, room, "Use the prepared dependency and record the result").Runs[0].ID)
	if result.Status != "succeeded" || result.TestExitCode == nil || *result.TestExitCode != 0 || !result.Published || len(result.Files) != 1 || result.Files[0].Path != "result.md" || !strings.Contains(result.Output, "12.11.2") {
		t.Fatalf("pnpm run: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(source, "node_modules")); !os.IsNotExist(err) {
		t.Fatal("source directory modified")
	}
}
