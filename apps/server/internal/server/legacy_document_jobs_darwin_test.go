//go:build darwin

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/nativejob"
	"github.com/cofy-x/camp/apps/server/internal/processowner"
)

func legacyDocumentFixture(t *testing.T) (data, job string) {
	t.Helper()
	data, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	job = filepath.Join(data, "document-jobs", "reader-retained", "jobs", "job-retained")
	if err := os.MkdirAll(job, 0700); err != nil {
		t.Fatal(err)
	}
	client := processowner.Identity{PID: 987654, Version: 1, UniqueID: 2, Coalition: 3, Boot: "retained-boot"}
	spec := nativejob.Spec{Argv: []string{"/removed/old-camp", "document-worker"}, Helper: "/removed/old-helper", Directory: filepath.Join(data, "document-jobs", "reader-retained", "work"), TimeoutMS: 30000, MaxOutput: (8 << 20) + 8192}
	writeLegacyReceipt(t, filepath.Join(job, "request.json"), spec)
	spec.Client, spec.Domain = client, fmt.Sprintf("user/%d", os.Getuid())
	sum := sha256.Sum256([]byte(filepath.Join(job, "request.json")))
	spec.Label = fmt.Sprintf("dev.cofy.camp.job.%x", sum[:12])
	writeLegacyReceipt(t, filepath.Join(job, "launch.json"), spec)
	writeLegacyReceipt(t, filepath.Join(job, "client.json"), client)
	writeLegacyReceipt(t, filepath.Join(job, "owner.json"), processowner.Identity{PID: 987655, Version: 2, UniqueID: 3, Coalition: 4, Boot: client.Boot})
	return data, job
}

func writeLegacyReceipt(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func legacyDocumentSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			value += fmt.Sprintf(" %x", sha256.Sum256(data))
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(name)
			if err != nil {
				return err
			}
			value += " " + target
		}
		result[name] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestLegacyDocumentRecoveryPreservesReceiptsWithoutReaderReplay(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleanup-error-%v", failed), func(t *testing.T) {
			data, job := legacyDocumentFixture(t)
			if failed {
				// Both historical reader shapes remain recognizable without any
				// old executable or runtime being installed or started.
				for _, name := range []string{"request.json", "launch.json"} {
					var spec nativejob.Spec
					if err := readLegacyDocumentReceipt(filepath.Join(job, name), &spec); err != nil {
						t.Fatal(err)
					}
					spec.Argv = []string{"/removed/old-node", "--max-old-space-size=128", "--max-semi-space-size=8", "/removed/runtime/extract.mjs"}
					writeLegacyReceipt(t, filepath.Join(job, name), spec)
				}
			}
			before := legacyDocumentSnapshot(t, data)
			var logs bytes.Buffer
			calls := 0
			recoverLegacyDocumentJobsWith(context.Background(), data, slog.New(slog.NewTextHandler(&logs, nil)), func(ctx context.Context, root string) error {
				calls++
				if root != filepath.Dir(job) {
					t.Fatalf("recovered another operation: %s", root)
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 8*time.Second {
					t.Fatal("legacy cleanup has no bounded deadline")
				}
				if failed {
					return errors.New("private path /hidden/credential must not be logged")
				}
				return nil
			})
			if calls != 1 || !reflect.DeepEqual(before, legacyDocumentSnapshot(t, data)) {
				t.Fatal("cleanup lost receipts, created a result/work directory, or did not reconcile the known job")
			}
			if strings.Contains(logs.String(), "credential") || strings.Contains(logs.String(), data) || !strings.Contains(logs.String(), "retained") {
				t.Fatalf("unsafe or missing retained-job warning: %s", logs.String())
			}
		})
	}
}

func TestLegacyDocumentRecoveryDoesNotConfirmCanceledCleanup(t *testing.T) {
	data, _ := legacyDocumentFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs bytes.Buffer
	recoverLegacyDocumentJobsWith(ctx, data, slog.New(slog.NewTextHandler(&logs, nil)), func(context.Context, string) error {
		cancel()
		return nil
	})
	if !strings.Contains(logs.String(), "cleanup-unconfirmed") || strings.Contains(logs.String(), "review-retained-receipts") {
		t.Fatalf("a canceled cleanup was treated as completed: %s", logs.String())
	}
}

func TestLegacyDocumentRecoveryRejectsUnknownOrUnsafeStorage(t *testing.T) {
	for _, name := range []string{"root-mode", "root-symlink", "reader-symlink", "unknown-reader", "job-symlink", "receipt-symlink", "receipt-hardlink", "receipt-mode", "oversized-receipt", "malformed-owner", "missing-launch", "foreign-label", "client-mismatch", "non-reader-command"} {
		t.Run(name, func(t *testing.T) {
			data, job := legacyDocumentFixture(t)
			root := filepath.Join(data, "document-jobs")
			switch name {
			case "root-mode":
				if err := os.Chmod(root, 0755); err != nil {
					t.Fatal(err)
				}
			case "root-symlink", "reader-symlink", "job-symlink":
				selected := root
				if name == "reader-symlink" {
					selected = filepath.Dir(filepath.Dir(job))
				}
				if name == "job-symlink" {
					selected = job
				}
				if err := os.Rename(selected, selected+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(selected+"-saved", selected); err != nil {
					t.Fatal(err)
				}
			case "unknown-reader":
				if err := os.Mkdir(filepath.Join(root, "unknown"), 0700); err != nil {
					t.Fatal(err)
				}
			case "receipt-symlink", "receipt-hardlink":
				file := filepath.Join(job, "owner.json")
				if err := os.Rename(file, filepath.Join(data, "saved-owner")); err != nil {
					t.Fatal(err)
				}
				link := os.Symlink
				if name == "receipt-hardlink" {
					link = os.Link
				}
				if err := link(filepath.Join(data, "saved-owner"), file); err != nil {
					t.Fatal(err)
				}
			case "receipt-mode":
				if err := os.Chmod(filepath.Join(job, "owner.json"), 0644); err != nil {
					t.Fatal(err)
				}
			case "oversized-receipt":
				if err := os.WriteFile(filepath.Join(job, "owner.json"), bytes.Repeat([]byte("x"), (64<<10)+1), 0600); err != nil {
					t.Fatal(err)
				}
			case "malformed-owner":
				writeLegacyReceipt(t, filepath.Join(job, "owner.json"), map[string]any{"pid": 1})
			case "missing-launch":
				if err := os.Remove(filepath.Join(job, "launch.json")); err != nil {
					t.Fatal(err)
				}
			default:
				var spec nativejob.Spec
				if err := readLegacyDocumentReceipt(filepath.Join(job, "launch.json"), &spec); err != nil {
					t.Fatal(err)
				}
				switch name {
				case "foreign-label":
					spec.Label = "unowned-service"
				case "client-mismatch":
					spec.Client.Version++
				case "non-reader-command":
					spec.Argv = []string{"/bin/sh", "-c", "echo never-run"}
					var request nativejob.Spec
					if err := readLegacyDocumentReceipt(filepath.Join(job, "request.json"), &request); err != nil {
						t.Fatal(err)
					}
					request.Argv = spec.Argv
					writeLegacyReceipt(t, filepath.Join(job, "request.json"), request)
				}
				writeLegacyReceipt(t, filepath.Join(job, "launch.json"), spec)
			}
			before := legacyDocumentSnapshot(t, data)
			var logs bytes.Buffer
			recoverLegacyDocumentJobsWith(context.Background(), data, slog.New(slog.NewTextHandler(&logs, nil)), func(context.Context, string) error {
				t.Fatal("unsafe or unknown ownership reached native recovery")
				return nil
			})
			if !reflect.DeepEqual(before, legacyDocumentSnapshot(t, data)) || !strings.Contains(logs.String(), "retained") {
				t.Fatal("unknown legacy operation was changed or not reported")
			}
		})
	}
}

func TestLegacyDocumentRecoveryDoesNotCreateStorageOrExceedInventory(t *testing.T) {
	data := t.TempDir()
	recoverLegacyDocumentJobs(context.Background(), data, nil)
	if _, err := os.Lstat(filepath.Join(data, "document-jobs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery created document storage: %v", err)
	}
	data, _ = legacyDocumentFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	never := func(context.Context, string) error {
		t.Fatal("canceled or excessive inventory reached cleanup")
		return nil
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	recoverLegacyDocumentJobsWith(ctx, data, logger, never)
	for i := 0; i < legacyDocumentInventoryLimit; i++ {
		if err := os.Mkdir(filepath.Join(data, "document-jobs", fmt.Sprintf("reader-%d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	before := legacyDocumentSnapshot(t, data)
	recoverLegacyDocumentJobsWith(context.Background(), data, logger, never)
	if !reflect.DeepEqual(before, legacyDocumentSnapshot(t, data)) || !strings.Contains(logs.String(), "unsafe-reader-inventory") {
		t.Fatal("excessive legacy inventory was not retained for inspection")
	}
}
