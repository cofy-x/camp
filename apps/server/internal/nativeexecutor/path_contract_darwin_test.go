//go:build darwin

package nativeexecutor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/runnertools"
	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

func TestNativeFileDiscoveryAndShellShareTheAdvertisedWorkspace(t *testing.T) {
	provider, ctx, root := nativeProvider(t)
	source, refs := filepath.Join(root, "source"), filepath.Join(root, "references")
	for _, dir := range []string{source, refs} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "brief.txt"), []byte("source evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refs, "evidence.txt"), []byte("immutable reference"), 0600); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(root, "path-contract.mjs")
	if err := os.WriteFile(driver, []byte(nativePathContractDriver), 0600); err != nil {
		t.Fatal(err)
	}
	previous := ""
	for index := range 2 {
		active, err := provider.Prepare(ctx, workspace.Spec{Source: source, References: refs, NamespaceKey: digest(fmt.Sprintf("native-guidance-%d", index))})
		if err != nil {
			t.Fatal(err)
		}
		tools, err := runnertools.NewTools(ctx, active, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tools.Close(); _ = active.Close(context.Background()) })
		_, referenceRoot := active.(*nativeWorkspace).PathRoots()
		command := exec.CommandContext(ctx, provider.config.NodePath, driver, provider.config.RuntimeDir)
		command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "LANG=C.UTF-8", "CAMP_TOOL_ENDPOINT=" + tools.Endpoint(), "CAMP_TOOL_TOKEN=" + tools.Token(), "EXPECTED_WORKSPACE=" + active.Directory(), "EXPECTED_REFERENCES=" + referenceRoot, "PREVIOUS_WORKSPACE=" + previous}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("native discovery/command contract: %v\n%s", err, output)
		}
		if err := tools.Close(); err != nil {
			t.Fatal(err)
		}
		capture := filepath.Join(root, fmt.Sprintf("capture-%d.tar", index))
		if _, err := active.Capture(ctx, capture, "Path contract", nil); err != nil {
			t.Fatal(err)
		}
		if err := active.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if active.Directory() == previous {
			t.Fatal("the namespace fixture did not change")
		}
		previous = active.Directory()
	}
	for name, want := range map[string]string{filepath.Join(source, "brief.txt"): "source evidence", filepath.Join(refs, "evidence.txt"): "immutable reference"} {
		data, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(data, []byte(want)) {
			t.Fatal("an immutable input changed", err)
		}
	}
}

const nativePathContractDriver = `
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {realpathSync} from 'node:fs';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
const runtime=process.argv[2];
const require=createRequire(realpathSync(join(runtime,'node_modules/@deepseek-ai/dsh-fs/package.json')));
const {Context}=await import(pathToFileURL(require.resolve('@deepseek-ai/cordis')));
const moduleURL=name=>pathToFileURL(join(runtime,'camp-tools',name+'.mjs'));
const {default:FileSystem}=await import(moduleURL('filesystem'));
const {default:Shell}=await import(moduleURL('shell'));
const {apply,listFiles,searchFiles}=await import(moduleURL('discovery'));
const ctx=new Context(),fs=new FileSystem(ctx),shell=new Shell(ctx);
const root=await fs.resolve('.');
assert.equal(root.displayPath,process.env.EXPECTED_WORKSPACE);
assert.deepEqual(await fs.resolve('/workspace'),root);
const registered=[];apply({fs,tools:{register:tool=>registered.push(tool)}});
for(const tool of registered){assert.match(tool.parameters.properties.path.description,/default '\.'/);assert(!tool.parameters.properties.path.description.includes('/workspace'));}
assert.match((await listFiles(fs,{})).text,/brief.txt/);
assert.match((await searchFiles(fs,{query:'source evidence'})).text,/brief.txt:1: source evidence/);
const reference=await fs.resolve(process.env.EXPECTED_REFERENCES+'/evidence.txt');
assert.equal(await fs.readText(reference),'immutable reference');
await assert.rejects(fs.writeText(reference,'changed',{kind:'createIfAbsent'}));
const target=await fs.resolve('result.txt');
await fs.writeText(target,'guided result',{kind:'createIfAbsent'});
const command=await shell.execute({command:'pwd; cat result.txt'});
const result=await command.result();
assert.equal(result.exitCode,0);
assert.equal(result.stdout.text,root.displayPath+'\nguided result');
assert.equal(await fs.readText(target),'guided result');
if(process.env.PREVIOUS_WORKSPACE)await assert.rejects(fs.resolve(process.env.PREVIOUS_WORKSPACE+'/brief.txt'));
`
