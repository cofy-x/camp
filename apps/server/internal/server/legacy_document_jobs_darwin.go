//go:build darwin

package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/nativejob"
	"github.com/cofy-x/camp/apps/server/internal/processowner"
	"golang.org/x/sys/unix"
)

const legacyDocumentInventoryLimit = 256

// Only old authenticated jobs are reconciled. Parsing, work directories and
// success receipts are never created; all historical evidence stays in place.
func recoverLegacyDocumentJobs(ctx context.Context, dataDir string, logger *slog.Logger) {
	recoverLegacyDocumentJobsWith(ctx, dataDir, logger, nativejob.Recover)
}

func recoverLegacyDocumentJobsWith(ctx context.Context, dataDir string, logger *slog.Logger, recoverJobs func(context.Context, string) error) {
	if logger == nil {
		logger = slog.Default()
	}
	warn := func(reason string) {
		// Inventory errors may contain private paths or receipt contents.
		logger.Warn("Legacy document reader jobs are retained; automatic document parsing is disabled", "reason", reason)
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	base, err := filepath.Abs(dataDir)
	if err == nil {
		base, err = filepath.EvalSymlinks(base)
	}
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		warn("unsafe-reader-inventory")
		return
	}
	root := filepath.Join(base, "document-jobs")
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return
	}
	readers, err := legacyDocumentDirectories(root, "reader-", legacyDocumentInventoryLimit)
	if err != nil {
		warn("unsafe-reader-inventory")
		return
	}
	remaining := legacyDocumentInventoryLimit
	for _, reader := range readers {
		if ctx.Err() != nil {
			warn("cleanup-unconfirmed")
			return
		}
		jobs := filepath.Join(root, reader, "jobs")
		if _, err := os.Lstat(jobs); errors.Is(err, os.ErrNotExist) {
			// A pre-admission operation may contain only its work directory.
			continue
		}
		entries, err := legacyDocumentDirectories(jobs, "job-", remaining)
		if err != nil {
			warn("unsafe-job-inventory")
			continue
		}
		remaining -= len(entries)
		known := true
		for _, entry := range entries {
			if ctx.Err() != nil || validateLegacyDocumentJob(filepath.Join(jobs, entry), filepath.Join(root, reader, "work")) != nil {
				known = false
				break
			}
		}
		if !known {
			warn("unknown-job-ownership")
			continue
		}
		if len(entries) > 0 {
			if err := recoverJobs(ctx, jobs); err != nil || ctx.Err() != nil {
				warn("cleanup-unconfirmed")
			} else {
				// Recover never invents a lost result. Retention is independent of
				// whether its bounded cleanup returned an error.
				warn("review-retained-receipts")
			}
		}
	}
}

func legacyDocumentDirectories(directory, prefix string, limit int) ([]string, error) {
	if err := legacyDocumentDirectory(directory); err != nil {
		return nil, err
	}
	file, err := os.Open(directory)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	entries, err := file.ReadDir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > limit {
		return nil, errors.New("Legacy document inventory exceeded its bound")
	}
	var result []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) || len(entry.Name()) == len(prefix) || legacyDocumentDirectory(filepath.Join(directory, entry.Name())) != nil {
			return nil, errors.New("Unrecognized legacy document directory")
		}
		result = append(result, entry.Name())
	}
	return result, nil
}

func legacyDocumentDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	canonical, resolveErr := filepath.EvalSymlinks(directory)
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || stat.Uid != uint32(os.Geteuid()) || resolveErr != nil || canonical != directory {
		return errors.New("Legacy document storage is not a private owned directory")
	}
	return nil
}

func readLegacyDocumentReceipt(name string, value any) error {
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() <= 0 || info.Size() > 64<<10 || !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return errors.New("Invalid legacy document receipt")
	}
	decoder := json.NewDecoder(io.LimitReader(file, (64<<10)+1))
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("Invalid legacy document receipt")
	}
	return nil
}

func validateLegacyDocumentJob(directory, work string) error {
	var request, launch nativejob.Spec
	var client processowner.Identity
	for _, receipt := range []struct {
		name  string
		value any
	}{{"request.json", &request}, {"launch.json", &launch}, {"client.json", &client}} {
		if err := readLegacyDocumentReceipt(filepath.Join(directory, receipt.name), receipt.value); err != nil {
			return err
		}
	}
	validIdentity := func(id processowner.Identity) bool {
		return id.PID > 1 && id.Version > 0 && id.UniqueID > 0 && id.Coalition > 0 && id.Boot != ""
	}
	sum := sha256.Sum256([]byte(filepath.Join(directory, "request.json")))
	if !validIdentity(client) || launch.Client != client {
		return errors.New("Invalid legacy document client")
	}
	if launch.Label != fmt.Sprintf("dev.cofy.camp.job.%x", sum[:12]) || launch.Domain != fmt.Sprintf("gui/%d", os.Getuid()) && launch.Domain != fmt.Sprintf("user/%d", os.Getuid()) {
		return errors.New("Invalid legacy document launch identity")
	}
	if request.Directory != work || launch.Directory != work || !slices.Equal(request.Argv, launch.Argv) || request.Helper != launch.Helper || !filepath.IsAbs(launch.Helper) {
		return errors.New("Inconsistent legacy document job")
	}
	if request.TimeoutMS != 30000 || launch.TimeoutMS != request.TimeoutMS || request.MaxOutput != (8<<20)+8192 || launch.MaxOutput != request.MaxOutput {
		return errors.New("Unrecognized legacy document job")
	}
	argv := request.Argv
	if len(argv) == 0 || !filepath.IsAbs(argv[0]) || !(len(argv) == 2 && argv[1] == "document-worker" || len(argv) == 4 && argv[1] == "--max-old-space-size=128" && argv[2] == "--max-semi-space-size=8" && filepath.IsAbs(argv[3]) && filepath.Base(argv[3]) == "extract.mjs") {
		return errors.New("Unrecognized legacy document reader")
	}
	for _, name := range []string{"owner.json", "worker-owner.json"} {
		if _, err := os.Lstat(filepath.Join(directory, name)); errors.Is(err, os.ErrNotExist) {
			continue
		}
		var owner processowner.Identity
		if err := readLegacyDocumentReceipt(filepath.Join(directory, name), &owner); err != nil || !validIdentity(owner) || owner.Boot != client.Boot || owner.Coalition == client.Coalition {
			return errors.New("Invalid legacy document owner")
		}
	}
	return nil
}
