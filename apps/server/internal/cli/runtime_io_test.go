package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeIOBoundsAndDrainsAdmittedWork(t *testing.T) {
	group := newRuntimeIO(context.Background(), runtimeIOConcurrency)
	release := make(chan struct{})
	started := make(chan struct{}, runtimeIOConcurrency)
	var active, peak, finished atomic.Int32
	for i := 0; i < runtimeIOConcurrency; i++ {
		if err := group.run(func() error {
			n := active.Add(1)
			for prior := peak.Load(); n > prior && !peak.CompareAndSwap(prior, n); prior = peak.Load() {
			}
			started <- struct{}{}
			<-release
			active.Add(-1)
			finished.Add(1)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < runtimeIOConcurrency; i++ {
		<-started
	}
	if len(group.slots) != runtimeIOConcurrency {
		t.Fatal("admission was not bounded")
	}
	group.cancel()
	if err := group.run(func() error { t.Error("admitted work after cancellation"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- group.wait() }()
	select {
	case <-done:
		t.Fatal("returned while admitted work still owned files")
	default:
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if peak.Load() != runtimeIOConcurrency || finished.Load() != runtimeIOConcurrency || active.Load() != 0 {
		t.Fatal("incorrect worker ownership", peak.Load(), finished.Load(), active.Load())
	}
}

func TestRuntimeIOPreservesFirstFailureAndClosesOtherWork(t *testing.T) {
	group := newRuntimeIO(context.Background(), 2)
	entered, release := make(chan struct{}), make(chan struct{})
	closed := false
	if err := group.run(func() error { close(entered); <-release; closed = true; return nil }); err != nil {
		t.Fatal(err)
	}
	<-entered
	want := errors.New("fixture disk synchronization failure")
	if err := group.run(func() error { return want }); err != nil {
		t.Fatal(err)
	}
	<-group.ctx.Done()
	close(release)
	if err := group.wait(); !errors.Is(err, want) || !closed {
		t.Fatal("lost root failure or failed to drain", err, closed)
	}
}

func TestRuntimeIOCallerFailureCancelsReadersBeforeDraining(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	group := newRuntimeIO(ctx, 1)
	entered := make(chan struct{})
	closed := false
	if err := group.run(func() error {
		close(entered)
		<-group.ctx.Done()
		closed = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	<-entered
	want := errors.New("fixture namespace validation failure")
	err := want
	done := make(chan struct{})
	go func() { group.finish(&err); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("caller failure did not cancel admitted readers before waiting")
	}
	if !errors.Is(err, want) || !closed {
		t.Fatal("caller error or cleanup lost", err, closed)
	}
}

// Explicit measurement writes only fresh temporary files. It compares the same
// streaming extractor at one/eight I/O slots; this is not a cold-install claim.
func TestRuntimeIOExtractionMeasurements(t *testing.T) {
	if os.Getenv("CAMP_TEST_RUNTIME_IO_BENCHMARK") != "1" {
		t.Skip("opt-in actual filesystem installation measurements")
	}
	var encoded bytes.Buffer
	compressed := gzip.NewWriter(&encoded)
	archive := tar.NewWriter(compressed)
	const files = 10000
	const fileBytes = 512
	for i := 0; i < files; i++ {
		digest := sha256.Sum256([]byte(fmt.Sprint(i)))
		content := bytes.Repeat(digest[:], fileBytes/len(digest))
		name := fmt.Sprintf("camp-runtime-darwin-arm64/runtime/files/%03d/file-%05d", i/100, i)
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		t.Fatal(err)
	}
	type sample struct {
		Round, Concurrency int
		ElapsedMS          float64
		Files              int
	}
	var samples []sample
	for round := 1; round <= 3; round++ {
		order := []int{1, runtimeIOConcurrency}
		if round%2 == 0 {
			order[0], order[1] = order[1], order[0]
		}
		for _, concurrency := range order {
			directory := t.TempDir()
			start := time.Now()
			if err := extractRuntimeIO(context.Background(), bytes.NewReader(encoded.Bytes()), directory, files*fileBytes, "darwin-arm64", nil, concurrency); err != nil {
				t.Fatal(err)
			}
			samples = append(samples, sample{round, concurrency, float64(time.Since(start)) / float64(time.Millisecond), files})
			for _, i := range []int{0, files / 2, files - 1} {
				name := filepath.Join(directory, fmt.Sprintf("runtime/files/%03d/file-%05d", i/100, i))
				actual, err := os.ReadFile(name)
				digest := sha256.Sum256([]byte(fmt.Sprint(i)))
				if err != nil || !bytes.Equal(actual, bytes.Repeat(digest[:], fileBytes/len(digest))) {
					t.Fatal("changed extracted bytes", i, err)
				}
			}
		}
	}
	data, err := json.Marshal(struct {
		Scope         string
		ArchiveSHA256 string
		Samples       []sample
	}{"three warm same-host 10000-file extraction rounds; includes complete file and directory synchronization; no model or installed runtime", fmt.Sprintf("%x", sha256.Sum256(encoded.Bytes())), samples})
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(data))
	if filename := os.Getenv("CAMP_RUNTIME_IO_REPORT"); filename != "" {
		file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(data)
		if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRuntimeIOVerificationMeasurements(t *testing.T) {
	if os.Getenv("CAMP_TEST_RUNTIME_IO_BENCHMARK") != "1" {
		t.Skip("opt-in actual filesystem verification measurements")
	}
	root := t.TempDir()
	manifest := bundleManifest{Schema: 2, Role: "runner-runtime", Platform: "darwin/arm64"}
	manifest.Files = make(map[string]struct {
		SHA256 string `json:"sha256"`
		Bytes  int64  `json:"bytes"`
	})
	names := []string{"bin/node", "bin/camp-sandbox", "runtime/dsh-runtime/package.json", "runtime/project-runtime/package.json"}
	for i := 0; i < 10000; i++ {
		names = append(names, fmt.Sprintf("runtime/dsh-runtime/node_modules/.pnpm/fixture@1/node_modules/fixture/files/%03d/%05d", i/100, i))
	}
	for _, name := range names {
		data := sha256.Sum256([]byte(name))
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data[:], 0600); err != nil {
			t.Fatal(err)
		}
		manifest.Files[name] = struct {
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		}{fmt.Sprintf("%x", sha256.Sum256(data[:])), int64(len(data))}
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(fmt.Sprintf("%x  manifest.json\n", sha256.Sum256(data))), 0600); err != nil {
		t.Fatal(err)
	}
	type sample struct {
		Round, Concurrency int
		ElapsedMS          float64
		Files              int
	}
	var samples []sample
	for round := 1; round <= 3; round++ {
		order := []int{1, runtimeIOConcurrency}
		if round%2 == 0 {
			order[0], order[1] = order[1], order[0]
		}
		for _, concurrency := range order {
			start := time.Now()
			if _, err := verifyRunnerBundleIO(context.Background(), root, nil, concurrency); err != nil {
				t.Fatal(err)
			}
			samples = append(samples, sample{round, concurrency, float64(time.Since(start)) / float64(time.Millisecond), len(names)})
		}
	}
	// Every sample verified all hashes and namespace membership. This corruption
	// must still fail the parallel verifier, regardless of successful old samples.
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(names[len(names)-1])), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyRunnerBundleIO(context.Background(), root, nil, runtimeIOConcurrency); err == nil {
		t.Fatal("reused old verification after content changed")
	}
	report, err := json.Marshal(struct {
		Scope          string
		ManifestSHA256 string
		Samples        []sample
	}{"three warm same-host complete synthetic runtime file/namespace verification rounds; no native execution or installation guarantee", fmt.Sprintf("%x", sha256.Sum256(data)), samples})
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(report))
	if filename := os.Getenv("CAMP_RUNTIME_IO_REPORT"); filename != "" {
		file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(report)
		if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
			t.Fatal(err)
		}
	}
}
