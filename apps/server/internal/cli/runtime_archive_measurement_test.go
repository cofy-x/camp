package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"testing"
	"time"
)

// Compare synchronization policies on one fixed archive, including all file and
// directory synchronization and independent full manifest verification. This is
// a filesystem measurement, not a Runner installation or cold-VM qualification.
func TestRuntimeArchiveMeasurements(t *testing.T) {
	archiveName, pinName := os.Getenv("CAMP_TEST_RUNTIME_ARCHIVE"), os.Getenv("CAMP_TEST_RUNTIME_PIN")
	if archiveName == "" && pinName == "" {
		t.Skip("opt-in fixed runtime archive filesystem measurements")
	}
	pinBytes, err := os.ReadFile(pinName)
	if err != nil {
		t.Fatal(err)
	}
	var pin runtimePin
	if err := json.Unmarshal(pinBytes, &pin); err != nil || pin.target() == "" || pin.Bytes <= 0 || pin.UnpackedBytes <= 0 || !digestPattern.MatchString(pin.SHA256) || !digestPattern.MatchString(pin.ManifestSHA256) {
		t.Fatal("invalid fixed archive pin", err)
	}
	type sample struct {
		Round     int     `json:"round"`
		Policy    string  `json:"policy"`
		ExtractMS float64 `json:"extractMs"`
		VerifyMS  float64 `json:"verifyMs"`
		TotalMS   float64 `json:"totalMs"`
		Files     int     `json:"files"`
		Links     int     `json:"links"`
	}
	var samples []sample
	for round := 1; round <= 3; round++ {
		order := []string{"per-entry-full-sync", "staged-full-sync"}
		if round%2 == 0 {
			order[0], order[1] = order[1], order[0]
		}
		for _, policy := range order {
			archive, err := os.Open(archiveName)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = archive.Close() })
			hash := sha256.New()
			size, hashErr := io.Copy(hash, archive)
			if hashErr != nil || size != pin.Bytes || hex.EncodeToString(hash.Sum(nil)) != pin.SHA256 {
				_ = archive.Close()
				t.Fatal("fixed archive changed before measurement", hashErr)
			}
			if _, err := archive.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			sync := runtimeStagingSync{syncRuntimeEntry, syncRuntimeStage}
			if policy == "per-entry-full-sync" {
				sync.entry = func(file *os.File) error { return file.Sync() }
			}
			directory := t.TempDir()
			start := time.Now()
			err = extractRuntimeSynced(context.Background(), archive, directory, pin.UnpackedBytes, pin.target(), nil, runtimeIOConcurrency, sync)
			extracted := time.Now()
			if err := errors.Join(err, archive.Close()); err != nil {
				t.Fatal(err)
			}
			manifest, err := verifyPinnedRuntime(directory, pin)
			if err != nil {
				t.Fatal("independent runtime readback failed", err)
			}
			samples = append(samples, sample{round, policy, float64(extracted.Sub(start)) / float64(time.Millisecond), float64(time.Since(extracted)) / float64(time.Millisecond), float64(time.Since(start)) / float64(time.Millisecond), len(manifest.Files), len(manifest.Symlinks)})
			t.Logf("round=%d policy=%s extract=%s complete=%s files=%d links=%d", round, policy, extracted.Sub(start), time.Since(start), len(manifest.Files), len(manifest.Symlinks))
			// Cleanup stays outside measurements; successful fixtures contain no data.
			if err := os.RemoveAll(directory); err != nil {
				t.Fatal(err)
			}
		}
	}
	data, err := json.Marshal(struct {
		Schema  int        `json:"schema"`
		Scope   string     `json:"scope"`
		Go      string     `json:"go"`
		Host    string     `json:"host"`
		Slots   int        `json:"slots"`
		Pin     runtimePin `json:"pin"`
		Samples []sample   `json:"samples"`
	}{1, "three interleaved same-archive policy comparisons; includes final full flush and readback; archive prehash outside timing; warm caches; no VM/model/installed identity", runtime.Version(), runtime.GOOS + "/" + runtime.GOARCH, runtimeIOConcurrency, pin, samples})
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(data))
	if name := os.Getenv("CAMP_RUNTIME_IO_REPORT"); name != "" {
		file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(data)
		if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
			t.Fatal(err)
		}
	}
}
