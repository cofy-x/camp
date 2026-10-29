package nativeexecutor

import (
	"archive/tar"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSelfTestRequiresCapturedResult(t *testing.T) {
	for _, text := range []string{"Camp Runner self-test\n", "wrong"} {
		name := filepath.Join(t.TempDir(), "result.tar")
		file, err := os.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		writer := tar.NewWriter(file)
		if err = writer.WriteHeader(&tar.Header{Name: "result.txt", Mode: 0600, Size: int64(len(text))}); err != nil {
			t.Fatal(err)
		}
		if _, err = writer.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		file.Close()
		err = checkSelfTestArchive(name)
		if (err == nil) != (text == "Camp Runner self-test\n") {
			t.Fatalf("Captured self-test validation: %q %v", text, err)
		}
	}
}
func TestNativeReadinessSelfTest(t *testing.T) {
	layerRuntime, dshRuntime := os.Getenv("CAMP_TEST_LAYERFS_RUNTIME"), os.Getenv("CAMP_TEST_DSH_RUNTIME")
	if layerRuntime == "" || dshRuntime == "" {
		t.Skip("Set native runtime paths for isolated Linux self-test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	helper := filepath.Join(data, "camp-sandbox")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", helper, "../../cmd/camp-sandbox")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Build helper: %v %s", err, output)
	}
	executor, err := Open(ctx, Config{DataDir: data, RuntimeDir: dshRuntime, LayerFSRuntimeDir: layerRuntime, SandboxHelper: helper, NodePath: node})
	if err != nil {
		t.Fatal(err)
	}
	report := executor.SelfTest(ctx)
	if !report.Ready {
		t.Errorf("Self-test failed: %+v", report)
	}
	if err = executor.Close(); err != nil {
		t.Fatal(err)
	}
	for _, check := range report.Checks {
		if check.Status != "passed" {
			t.Errorf("Check failed: %+v", check)
		}
	}
}
