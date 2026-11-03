//go:build darwin

package nativeexecutor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func nativeComparisonPlanFixture(t *testing.T) (string, nativeComparisonPlan) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(root, "native-plan.json")
	if err := os.WriteFile(plan, []byte("controlled plan"), 0600); err != nil {
		t.Fatal(err)
	}
	input := nativeComparisonPlan{Schema: 1}
	for iteration := 1; iteration <= 3; iteration++ {
		for _, workload := range []string{"text", "10000-files", "128-MiB-binary", "dependencies-links"} {
			source := filepath.Join(root, fmt.Sprintf("round-%d", iteration), workload, "source")
			if err := os.MkdirAll(source, 0700); err != nil {
				t.Fatal(err)
			}
			for _, phase := range []string{"full", "hot", "incremental", "unchanged"} {
				input.Fixtures = append(input.Fixtures, nativeComparisonFixture{Workload: workload, Iteration: iteration, Phase: phase, Source: source, Digest: strings.Repeat("a", 64)})
			}
		}
	}
	return plan, input
}

func TestNativeComparisonPlanRequiresCompleteSingleOrThreeRoundMatrices(t *testing.T) {
	plan, input := nativeComparisonPlanFixture(t)
	if err := validateNativeComparisonPlan(plan, input); err != nil {
		t.Fatal("rejected complete legacy aggregate", err)
	}
	for iteration := 0; iteration < 3; iteration++ {
		round := nativeComparisonPlan{Schema: 1, Fixtures: input.Fixtures[16*iteration : 16*(iteration+1)]}
		if err := validateNativeComparisonPlan(plan, round); err != nil {
			t.Fatal("rejected complete independent round", iteration+1, err)
		}
	}
	for _, mutate := range []func(*nativeComparisonPlan){
		func(p *nativeComparisonPlan) { p.Schema = 0 },
		func(p *nativeComparisonPlan) { p.Fixtures = p.Fixtures[:1] },
		func(p *nativeComparisonPlan) { p.Fixtures = p.Fixtures[:15] },
		func(p *nativeComparisonPlan) { p.Fixtures = p.Fixtures[:32] },
		func(p *nativeComparisonPlan) { p.Fixtures[1] = p.Fixtures[0] },
		func(p *nativeComparisonPlan) { p.Fixtures[0].Iteration = 4 },
		func(p *nativeComparisonPlan) { p.Fixtures[0].Workload = "unexpected" },
		func(p *nativeComparisonPlan) { p.Fixtures[0].Phase = "restore" },
		func(p *nativeComparisonPlan) { p.Fixtures[0].Digest = strings.Repeat("A", 64) },
		func(p *nativeComparisonPlan) { p.Fixtures[0].Digest = "aabb" },
		func(p *nativeComparisonPlan) { p.Fixtures = p.Fixtures[:16]; p.Fixtures[0] = input.Fixtures[16] },
	} {
		changed := nativeComparisonPlan{Schema: input.Schema, Fixtures: append([]nativeComparisonFixture(nil), input.Fixtures...)}
		mutate(&changed)
		if err := validateNativeComparisonPlan(plan, changed); err == nil {
			t.Fatal("accepted incomplete or conflicting comparison matrix")
		}
	}
}

func TestNativeComparisonPlanBindsCanonicalSourceToItsRetainedRound(t *testing.T) {
	plan, input := nativeComparisonPlanFixture(t)
	root := filepath.Dir(plan)
	out := filepath.Join(root, "unrelated-input")
	if err := os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "round-1", "alias")
	if err := os.Symlink(input.Fixtures[0].Source, alias); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "round-1", "regular-file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"relative/source", input.Fixtures[16].Source, out, alias, file, filepath.Join(root, "round-1"), filepath.Join(root, "round-1", "missing")} {
		changed := nativeComparisonPlan{Schema: 1, Fixtures: append([]nativeComparisonFixture(nil), input.Fixtures...)}
		changed.Fixtures[0].Source = source
		if err := validateNativeComparisonPlan(plan, changed); err == nil {
			t.Fatal("accepted escaped, aliased or missing source", source)
		}
	}
	// Restored delta fixtures need not repeat the original workload directory.
	delta := filepath.Join(root, "round-1", "delta-sources", "restored-fixture")
	if err := os.MkdirAll(delta, 0700); err != nil {
		t.Fatal(err)
	}
	input.Fixtures[2].Source = delta
	if err := validateNativeComparisonPlan(plan, input); err != nil {
		t.Fatal("rejected retained canonical delta input", err)
	}
}

func TestNativeComparisonOutputNeverReplacesPriorEvidence(t *testing.T) {
	name := filepath.Join(t.TempDir(), "report.json")
	file, err := reserveNativeComparisonOutput(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("prior retained evidence")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reserveNativeComparisonOutput(name); !errors.Is(err, os.ErrExist) {
		t.Fatal("replaced prior evidence", err)
	}
	if data, err := os.ReadFile(name); err != nil || string(data) != "prior retained evidence" {
		t.Fatal("changed prior report", err)
	}
	if _, err := reserveNativeComparisonOutput("relative-report.json"); err == nil {
		t.Fatal("accepted an implicit output location")
	}
}

func TestNativeComparisonUnsupportedRequiresExactKnownLinkBoundary(t *testing.T) {
	source := t.TempDir()
	item := nativeComparisonFixture{Workload: "dependencies-links", Source: source}
	if err := os.Symlink("/opaque/not-authorized", filepath.Join(source, "opaque-link")); err != nil {
		t.Fatal(err)
	}
	known := errors.New("Prepared dependency link escapes the project")
	if unsupported, err := nativeComparisonOutcome(item, known, nil, nil, nil); !unsupported || err == nil {
		t.Fatal("lost explicit production capability boundary", unsupported, err)
	}
	for _, failure := range []struct{ prepare, capture, close, sampling error }{
		{context.Canceled, nil, nil, nil},
		{os.ErrPermission, nil, nil, nil},
		{known, nil, errors.New("cleanup ownership unknown"), nil},
		{known, nil, nil, errors.New("RSS unavailable")},
		{nil, known, nil, nil},
	} {
		if unsupported, err := nativeComparisonOutcome(item, failure.prepare, failure.capture, failure.close, failure.sampling); unsupported || err == nil {
			t.Fatal("masked a failed comparison as unsupported", unsupported, err)
		}
	}
	item.Workload = "text"
	if unsupported, _ := nativeComparisonOutcome(item, known, nil, nil, nil); unsupported {
		t.Fatal("excused a different workload")
	}
	item.Workload, item.Source = "dependencies-links", t.TempDir()
	if unsupported, _ := nativeComparisonOutcome(item, known, nil, nil, nil); unsupported {
		t.Fatal("declared an unobserved opaque link unsupported")
	}
}

func TestNativeComparisonInputHashesActualRetainedBytes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("input digest integration requires the pinned Node toolchain")
	}
	controller, err := filepath.Abs("../../../../tools/layerfs-worker/tree.mjs")
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	name := filepath.Join(source, "source.md")
	if err := os.WriteFile(name, []byte("original retained input"), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, "--input-type=module", "-e", `import { pathToFileURL } from 'node:url';
const { scanTree } = await import(pathToFileURL(process.argv[1]));
process.stdout.write((await scanTree(process.argv[2])).digest);`, controller, source)
	bytes, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := nativeComparisonVerifierInputs(node, controller)
	if err != nil {
		t.Fatal(err)
	}
	item := nativeComparisonFixture{Source: source, Digest: string(bytes)}
	if err := verifyNativeComparisonInput(context.Background(), verifier, item); err != nil {
		t.Fatal("rejected matching retained input", err)
	}
	if err := os.WriteFile(name, []byte("changed retained input"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyNativeComparisonInput(context.Background(), verifier, item); err == nil {
		t.Fatal("echoed a stale expected digest without reading actual bytes")
	}
}

func TestNativeComparisonKeepsFailedWorkspacesAfterTestCleanup(t *testing.T) {
	var root string
	t.Run("failed observation", func(t *testing.T) {
		var err error
		root, err = nativeComparisonWorkspace()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "ownership-receipt"), []byte("keep exact evidence"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := completeNativeComparisonWorkspace(root, errors.New("cleanup ownership unknown")); err != nil {
			t.Fatal(err)
		}
	})
	t.Cleanup(func() { os.RemoveAll(root) }) // This test owns only its synthetic marker.
	if bytes, err := os.ReadFile(filepath.Join(root, "ownership-receipt")); err != nil || string(bytes) != "keep exact evidence" {
		t.Fatal("Go test cleanup removed failed comparison evidence", err)
	}
	if err := completeNativeComparisonWorkspace(root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("confirmed successful workspace was not removed", err)
	}
}

func TestNativeComparisonVerifierBindsNodeAndScannerInputs(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("verifier integration requires the pinned Node toolchain")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	controller := filepath.Join(root, "tree.mjs")
	dependency := filepath.Join(root, "state.mjs")
	for _, name := range []string{controller, dependency} {
		if err := os.WriteFile(name, []byte("export const value=1;\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	verifier, err := nativeComparisonVerifierInputs(node, controller)
	if err != nil || len(verifier.Files) != 3 || verifier.NodeVersion == "" {
		t.Fatal("lost actual scanner or Node identity", verifier, err)
	}
	changedNode := verifier
	changedNode.Files = map[string]string{}
	for name, hash := range verifier.Files {
		changedNode.Files[name] = hash
	}
	changedNode.Files[verifier.Node] = strings.Repeat("0", 64)
	if err := verifyNativeComparisonVerifier(changedNode, true); err == nil {
		t.Fatal("accepted a different Node executable digest")
	}
	for _, name := range []string{controller, dependency} {
		if err := os.WriteFile(name, []byte("export const value=2;\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := verifyNativeComparisonVerifier(verifier, false); err == nil {
			t.Fatal("accepted changed scanner code", name)
		}
		if err := os.WriteFile(name, []byte("export const value=1;\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fakeNode := filepath.Join(root, "node")
	if err := os.WriteFile(fakeNode, []byte("#!/bin/sh\nprintf 'v0.0.0\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeComparisonVerifierInputs(fakeNode, controller); err == nil {
		t.Fatal("accepted an unpinned scanner Node version")
	}
}
