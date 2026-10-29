package layerfs

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

func fixtureBridge(t *testing.T) (BridgeConfig, *Bridge) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the JSON-lines bridge transport test")
	}
	root := t.TempDir()
	script := `import {readFile,appendFile,stat} from 'node:fs/promises';
import {fstatSync} from 'node:fs';
import {createInterface} from 'node:readline';
const config = JSON.parse(await readFile(process.argv[2], 'utf8'));
let prepared = 0;
for await (const line of createInterface({input:process.stdin})) {
 const request = JSON.parse(line);
 let result = {secretInherited: process.env.CAMP_PRIVATE_MODEL_TEST !== undefined, configMode:(await stat(process.argv[2])).mode & 511, lockInherited:fstatSync(3).isFile()};
 if (request.method === 'shutdown') { console.log(JSON.stringify({id:request.id,result:{closed:true}})); break; }
 if (request.method === 'project.prepare') {
   prepared++;
   await appendFile(config.home+'/effects',request.params.sourceDir+'\n');
   if (request.params.sourceDir === 'slow') await new Promise(resolve=>setTimeout(resolve,250));
   if (request.params.sourceDir === 'malformed') { console.log('{broken'); continue; }
   result={projectId:'project-'+prepared,baselineKey:request.params.baselineKey};
 }
 console.log(JSON.stringify({id:request.id,result}));
}
`
	if err := os.WriteFile(filepath.Join(root, "bridge.mjs"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	config := BridgeConfig{Node: node, RuntimeDir: root, DataRoot: root, Home: filepath.Join(root, "home")}
	bridge, err := OpenBridge(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.Close() })
	return config, bridge
}

func TestBridgePrivateConfigurationAndExclusiveOwner(t *testing.T) {
	t.Setenv("CAMP_PRIVATE_MODEL_TEST", "never-forward-this")
	config, bridge := fixtureBridge(t)
	status, err := bridge.Status(context.Background())
	if err != nil || !strings.Contains(string(status), `"secretInherited":false`) || !strings.Contains(string(status), `"configMode":384`) || !strings.Contains(string(status), `"lockInherited":true`) {
		t.Fatalf("unexpected environment/config permissions: %s, %v", status, err)
	}
	if other, err := OpenBridge(context.Background(), config); err == nil {
		_ = other.Close()
		t.Fatal("a second bridge was allowed to own the same home")
	}
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bridge.config); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private configuration was not removed: %v", err)
	}
	if bridge.cmd.ProcessState == nil || !bridge.cmd.ProcessState.Exited() {
		t.Fatal("bridge process was not reaped")
	}
	other, err := OpenBridge(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeCancellationDrainsWithoutRepeatingOrMisrouting(t *testing.T) {
	config, bridge := fixtureBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err := bridge.Prepare(ctx, PrepareRequest{SourceDir: "slow", BaselineKey: "first"})
	var unknown *OutcomeUnknownError
	if !errors.As(err, &unknown) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected unknown dispatched outcome, got %v", err)
	}
	result, err := bridge.Prepare(context.Background(), PrepareRequest{SourceDir: "next", BaselineKey: "second"})
	if err != nil || result.ProjectID != "project-2" || result.BaselineKey != "second" {
		t.Fatalf("late response was misrouted: %+v, %v", result, err)
	}
	effects, err := os.ReadFile(filepath.Join(config.Home, "effects"))
	if err != nil || string(effects) != "slow\nnext\n" {
		t.Fatalf("operation was repeated or dropped: %q, %v", effects, err)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	_, err = bridge.Prepare(cancelled, PrepareRequest{SourceDir: "never", BaselineKey: "third"})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	effects, _ = os.ReadFile(filepath.Join(config.Home, "effects"))
	if strings.Contains(string(effects), "never") {
		t.Fatal("an undispatched cancelled request was executed")
	}
}

func TestBridgeInvalidOutputFailsAndReapsProcess(t *testing.T) {
	_, bridge := fixtureBridge(t)
	_, err := bridge.Prepare(context.Background(), PrepareRequest{SourceDir: "malformed", BaselineKey: "first"})
	var unknown *OutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("invalid protocol should have unknown outcome, got %v", err)
	}
	if err := bridge.Close(); err == nil {
		t.Fatal("invalid protocol exit was reported as clean")
	}
	if bridge.cmd.ProcessState == nil {
		t.Fatal("failed process was not reaped")
	}
}

func TestBridgeRejectsSymlinkHomeBeforeCreation(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	_, err := OpenBridge(context.Background(), BridgeConfig{Node: "/usr/bin/node", RuntimeDir: root, DataRoot: root, Home: filepath.Join(root, "link", "new")})
	if err == nil {
		t.Fatal("outside home accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("outside directory was created")
	}
}
