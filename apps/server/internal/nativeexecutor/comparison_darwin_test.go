//go:build darwin

package nativeexecutor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

type nativeComparisonFixture struct {
	Workload  string `json:"workload"`
	Iteration int    `json:"iteration"`
	Phase     string `json:"phase"`
	Source    string `json:"source"`
	Digest    string `json:"digest"`
}

type nativeComparisonPlan struct {
	Schema   int                       `json:"schema"`
	Fixtures []nativeComparisonFixture `json:"fixtures"`
}

func validateNativeComparisonPlan(plan string, input nativeComparisonPlan) error {
	if input.Schema != 1 || len(input.Fixtures) != 16 && len(input.Fixtures) != 48 {
		return errors.New("Native comparison requires one complete 16-input round or all three complete rounds")
	}
	canonical, err := filepath.EvalSymlinks(plan)
	if err != nil {
		return err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return err
	}
	workloads := map[string]bool{"text": true, "10000-files": true, "128-MiB-binary": true, "dependencies-links": true}
	phases := map[string]bool{"full": true, "hot": true, "incremental": true, "unchanged": true}
	seen, rounds := map[string]bool{}, map[int]bool{}
	for _, item := range input.Fixtures {
		if item.Iteration < 1 || item.Iteration > 3 || !workloads[item.Workload] || !phases[item.Phase] {
			return errors.New("Invalid native comparison matrix entry")
		}
		key := fmt.Sprintf("%d:%s:%s", item.Iteration, item.Workload, item.Phase)
		if seen[key] {
			return errors.New("Duplicate native comparison matrix entry")
		}
		seen[key], rounds[item.Iteration] = true, true
		if data, err := hex.DecodeString(item.Digest); err != nil || len(data) != 32 || strings.ToLower(item.Digest) != item.Digest {
			return errors.New("Invalid retained input digest")
		}
		if !filepath.IsAbs(item.Source) || filepath.Clean(item.Source) != item.Source {
			return errors.New("Native comparison sources must be canonical absolute directories")
		}
		actual, err := filepath.EvalSymlinks(item.Source)
		if err != nil || actual != item.Source {
			return errors.New("Native comparison source aliases are not accepted")
		}
		info, err := os.Stat(actual)
		if err != nil || !info.IsDir() {
			return errors.New("Native comparison source is unavailable")
		}
		round := filepath.Join(filepath.Dir(canonical), fmt.Sprintf("round-%d", item.Iteration))
		relative, err := filepath.Rel(round, actual)
		if err != nil || relative == "." || !filepath.IsLocal(relative) {
			return errors.New("Native comparison input escapes its retained round")
		}
	}
	if len(input.Fixtures) == 16 && len(rounds) != 1 || len(input.Fixtures) == 48 && len(rounds) != 3 {
		return errors.New("Native comparison rounds are incomplete")
	}
	for round := range rounds {
		for workload := range workloads {
			for phase := range phases {
				if !seen[fmt.Sprintf("%d:%s:%s", round, workload, phase)] {
					return errors.New("Native comparison matrix is incomplete")
				}
			}
		}
	}
	return nil
}

func reserveNativeComparisonOutput(name string) (*os.File, error) {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return nil, errors.New("Choose a fresh absolute native comparison report path")
	}
	return os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
}

const nativeComparisonUnsupportedCode = "native-prepared-link-outside-project"

// A workload name never excuses I/O, timeout or cleanup errors. Only the
// fixture's explicit opaque outside link reaches this known Prepare boundary.
func nativeComparisonOutcome(item nativeComparisonFixture, prepareErr, captureErr, closeErr, samplingErr error) (bool, error) {
	err := errors.Join(prepareErr, captureErr, closeErr, samplingErr)
	if item.Workload != "dependencies-links" || prepareErr == nil || prepareErr.Error() != "Prepared dependency link escapes the project" || captureErr != nil || closeErr != nil || samplingErr != nil {
		return false, err
	}
	link, linkErr := os.Readlink(filepath.Join(item.Source, "opaque-link"))
	return linkErr == nil && link == "/opaque/not-authorized", err
}

// A comparison owns these directories beyond the Go test's lifetime. Failed or
// uncertain work must survive test cleanup for inspection.
func nativeComparisonWorkspace() (string, error) {
	root, err := os.MkdirTemp("", "camp-native-comparison-")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(root)
}

func completeNativeComparisonWorkspace(root string, outcome error) error {
	if outcome != nil {
		return nil
	}
	return os.RemoveAll(root)
}

type nativeComparisonVerifier struct {
	Node        string            `json:"node"`
	NodeVersion string            `json:"nodeVersion"`
	Controller  string            `json:"controller"`
	Files       map[string]string `json:"files"`
}

func nativeComparisonFileHash(name string) (string, error) {
	canonical, err := filepath.EvalSymlinks(name)
	if err != nil || canonical != name {
		return "", errors.New("Comparison verifier requires canonical regular files")
	}
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("Comparison verifier requires regular files")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func nativeComparisonVerifierInputs(node, controller string) (nativeComparisonVerifier, error) {
	actual, err := filepath.EvalSymlinks(node)
	if err != nil {
		return nativeComparisonVerifier{}, err
	}
	input := nativeComparisonVerifier{Node: actual, Controller: controller, Files: map[string]string{}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, actual, "--version")
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	version, err := command.Output()
	input.NodeVersion = strings.TrimSpace(string(version))
	if err != nil || input.NodeVersion != "v"+projectenv.NodeVersion {
		return input, errors.New("Native comparison requires the pinned Node runtime")
	}
	for _, name := range []string{actual, controller, filepath.Join(filepath.Dir(controller), "state.mjs")} {
		input.Files[name], err = nativeComparisonFileHash(name)
		if err != nil {
			return input, err
		}
	}
	return input, nil
}

func verifyNativeComparisonVerifier(input nativeComparisonVerifier, includeNode bool) error {
	for name, expected := range input.Files {
		if name == input.Node && !includeNode {
			continue
		}
		actual, err := nativeComparisonFileHash(name)
		if err != nil || actual != expected {
			return errors.New("Native comparison verifier input changed")
		}
	}
	return nil
}

func verifyNativeComparisonInput(ctx context.Context, verifier nativeComparisonVerifier, item nativeComparisonFixture) error {
	if err := verifyNativeComparisonVerifier(verifier, false); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, verifier.Node, "--input-type=module", "-e", `import { pathToFileURL } from 'node:url';
const { scanTree } = await import(pathToFileURL(process.argv[1]));
process.stdout.write((await scanTree(process.argv[2])).digest);`, verifier.Controller, item.Source)
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	actual, err := command.Output()
	if err != nil {
		return fmt.Errorf("Verify retained native comparison input: %w", err)
	}
	if string(actual) != item.Digest {
		return errors.New("Retained native comparison input changed")
	}
	return verifyNativeComparisonVerifier(verifier, false)
}

func nativeComparisonProvider(t *testing.T, root, node string) (*nativeWorkspaceProvider, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	helper := os.Getenv("CAMP_TEST_SANDBOX_HELPER")
	if helper == "" {
		helper = filepath.Join(root, "camp-sandbox")
		if output, err := exec.CommandContext(ctx, "go", "build", "-o", helper, "../../cmd/camp-sandbox").CombinedOutput(); err != nil {
			t.Fatalf("helper: %v %s", err, output)
		}
	} else if !filepath.IsAbs(helper) {
		t.Fatal("Choose the verified absolute package helper")
	}
	config, err := normalizeConfig(Config{DataDir: root, RuntimeDir: os.Getenv("CAMP_TEST_DSH_RUNTIME"), SandboxHelper: helper, NodePath: node})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := openNativeWorkspace(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	return provider.(*nativeWorkspaceProvider), ctx
}

// This is the actual native-local production publication path. Its archive is
// deliberately distinct from the complete-tree capture/restore comparison.
func TestNativeLocalComparisonMeasurements(t *testing.T) {
	plan, output := os.Getenv("CAMP_MAC_COMPARISON_PLAN"), os.Getenv("CAMP_MAC_COMPARISON_OUTPUT")
	if plan == "" && output == "" {
		t.Skip("set explicit private Mac comparison plan and output")
	}
	if plan == "" || output == "" || os.Getenv("CAMP_TEST_DSH_RUNTIME") == "" {
		t.Fatal("Selected native comparison requires a plan, fresh output and fixed DSH runtime")
	}
	var input nativeComparisonPlan
	info, err := os.Lstat(plan)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		t.Fatal("invalid retained comparison plan", err)
	}
	data, err := os.ReadFile(plan)
	if err != nil || json.Unmarshal(data, &input) != nil {
		t.Fatal("invalid retained comparison plan", err)
	}
	if err := validateNativeComparisonPlan(plan, input); err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Native comparison requires the pinned Node toolchain", err)
	}
	controller, err := filepath.Abs("../../../../tools/layerfs-worker/tree.mjs")
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := nativeComparisonVerifierInputs(node, controller)
	if err != nil {
		t.Fatal(err)
	}
	file, err := reserveNativeComparisonOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	type observation struct {
		Fixture                    nativeComparisonFixture `json:"fixture"`
		PrepareMS                  float64                 `json:"prepareMs"`
		CaptureMS                  float64                 `json:"captureMs"`
		ArchiveBytes               int64                   `json:"archiveBytes"`
		ArchiveDigest              string                  `json:"archiveDigest,omitempty"`
		CPUUserUsec                int64                   `json:"cpuUserUsec"`
		CPUSystemUsec              int64                   `json:"cpuSystemUsec"`
		RSSKiB                     int                     `json:"rssKiB"`
		ProcessLifetimeMaxRSSBytes int64                   `json:"processLifetimeMaxRSSBytes"`
		Error                      string                  `json:"error,omitempty"`
		Unsupported                bool                    `json:"unsupported,omitempty"`
		UnsupportedCode            string                  `json:"unsupportedCode,omitempty"`
		InputVerificationBeforeMS  float64                 `json:"inputVerificationBeforeMs"`
		InputVerificationAfterMS   float64                 `json:"inputVerificationAfterMs"`
		RetainedWorkspace          string                  `json:"retainedWorkspace,omitempty"`
	}
	report := struct {
		Schema       int                      `json:"schema"`
		Passed       bool                     `json:"passed"`
		Scope        string                   `json:"scope"`
		Observations []observation            `json:"observations"`
		Verifier     nativeComparisonVerifier `json:"verifier"`
		Go           string                   `json:"go"`
		InputError   string                   `json:"inputError,omitempty"`
	}{Schema: 1, Verifier: verifier, Go: runtime.Version(), Scope: "actual native-local production Prepare and publication Capture on the identical retained inputs; input hashes checked before/after outside measured phases and may warm OS caches; excludes full-tree restore/fork, measured separately; per-process lifetime RSS peak and self CPU excluding children; existing VM/background load unchanged"}
	writeReport := func() {
		t.Helper()
		data, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			_, err = file.Seek(0, io.SeekStart)
		}
		if err == nil {
			err = file.Truncate(0)
		}
		if err == nil {
			_, err = file.Write(append(data, '\n'))
		}
		if err == nil {
			err = file.Sync()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	writeReport() // An interrupted/partial run never leaves a passing report.
	failed := false
	for _, item := range input.Fixtures {
		report.Observations = append(report.Observations, observation{Fixture: item})
		row := &report.Observations[len(report.Observations)-1]
		verify := func(elapsed *float64) error {
			started := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			err := verifyNativeComparisonInput(ctx, verifier, item)
			*elapsed = float64(time.Since(started).Microseconds()) / 1000
			return err
		}
		if err := verify(&row.InputVerificationBeforeMS); err != nil {
			row.Error, failed = err.Error(), true
			writeReport()
			continue
		}
		root, err := nativeComparisonWorkspace()
		if err != nil {
			t.Fatal(err)
		}
		row.RetainedWorkspace = root
		writeReport() // Record ownership before opening helpers or preparing work.
		provider, ctx := nativeComparisonProvider(t, root, verifier.Node)
		var before, after syscall.Rusage
		samplingErr := syscall.Getrusage(syscall.RUSAGE_SELF, &before)
		started := time.Now()
		active, prepareErr := provider.Prepare(ctx, workspace.Spec{Source: item.Source, AttemptID: fmt.Sprintf("comparison-%d-%s", item.Iteration, item.Phase)})
		row.PrepareMS = float64(time.Since(started).Microseconds()) / 1000
		var captureErr, closeErr error
		if prepareErr == nil {
			started = time.Now()
			archive, err := active.Capture(ctx, filepath.Join(root, "publication.tar"), "controlled comparison", []string{"node_modules"})
			row.CaptureMS = float64(time.Since(started).Microseconds()) / 1000
			row.ArchiveBytes, row.ArchiveDigest, captureErr = archive.Bytes, archive.SHA256, err
		}
		if active != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			closeErr = active.Close(cleanup)
			cancel()
			if closeErr != nil {
				t.Error(closeErr)
			}
		}
		samplingErr = errors.Join(samplingErr, syscall.Getrusage(syscall.RUSAGE_SELF, &after))
		row.CPUUserUsec = (after.Utime.Sec-before.Utime.Sec)*1_000_000 + int64(after.Utime.Usec-before.Utime.Usec)
		row.CPUSystemUsec = (after.Stime.Sec-before.Stime.Sec)*1_000_000 + int64(after.Stime.Usec-before.Stime.Usec)
		row.ProcessLifetimeMaxRSSBytes = after.Maxrss
		rss, rssErr := exec.Command("/bin/ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
		row.RSSKiB, err = strconv.Atoi(strings.TrimSpace(string(rss)))
		samplingErr = errors.Join(samplingErr, rssErr, err)
		if row.RSSKiB <= 0 || row.ProcessLifetimeMaxRSSBytes <= 0 {
			samplingErr = errors.Join(samplingErr, errors.New("Native comparison memory observation is unavailable"))
		}
		samplingErr = errors.Join(samplingErr, verify(&row.InputVerificationAfterMS))
		row.Unsupported, err = nativeComparisonOutcome(item, prepareErr, captureErr, closeErr, samplingErr)
		if err != nil {
			row.Error = err.Error()
			if row.Unsupported {
				row.UnsupportedCode = nativeComparisonUnsupportedCode
			} else {
				failed = true
			}
		}
		if cleanupErr := completeNativeComparisonWorkspace(root, err); cleanupErr != nil {
			row.Error, failed = cleanupErr.Error(), true
		} else if err == nil {
			row.RetainedWorkspace = ""
		}
		runtime.GC() // Outside each measured phase, as in the shipped baseline probe.
		writeReport()
	}
	if err := verifyNativeComparisonVerifier(verifier, true); err != nil {
		report.InputError, failed = err.Error(), true
	}
	report.Passed = !failed && len(report.Observations) == len(input.Fixtures)
	writeReport()
	if !report.Passed {
		t.Fatal("native-local comparison failed; all samples retained", output)
	}
}
