//go:build darwin

package documenttext

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/cofy-x/camp/apps/server/internal/nativejob"
	"github.com/cofy-x/camp/apps/server/internal/seatbelt"
)

func readerRoot(config Config) (string, error) {
	if !filepath.IsAbs(config.JobRoot) {
		return "", errors.New("Document readers require private Server job storage")
	}
	if err := os.MkdirAll(config.JobRoot, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(config.JobRoot)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("Document reader storage must be a private directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return "", errors.New("Document reader storage belongs to another account")
	}
	return filepath.EvalSymlinks(config.JobRoot)
}

func initializeReader(ctx context.Context, config Config) error {
	root, err := readerRoot(config)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) > 256 {
		return errors.New("Document reader recovery inventory needs inspection")
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "reader-") {
			return errors.New("Unrecognized document reader storage")
		}
		if err := nativejob.Recover(ctx, filepath.Join(root, entry.Name(), "jobs")); err != nil {
			return err
		}
	}
	return nil
}

func runReader(ctx context.Context, config Config, format string, input io.Reader, stdout, stderr io.Writer) error {
	helper, err := executable(config.HelperPath)
	if err != nil {
		return failure("unavailable", "The trusted document process helper is missing. Use a complete Camp Server package.", true)
	}
	root, err := readerRoot(config)
	if err != nil {
		return failure("unavailable", "Document reader storage is unavailable.", true)
	}
	operation, err := os.MkdirTemp(root, "reader-")
	if err != nil {
		return err
	}
	work := filepath.Join(operation, "work")
	if err = os.Mkdir(work, 0700); err != nil {
		return err
	}
	clean := true
	defer func() {
		if clean {
			_ = os.RemoveAll(operation)
		}
	}()
	spec := nativejob.Spec{
		Directory: work,
		Env:       []string{"PATH=/usr/bin:/bin", "HOME=" + work, "TMPDIR=" + work, "LANG=C.UTF-8", "GOMEMLIMIT=96MiB", "GOMAXPROCS=1"},
		Policy:    seatbelt.Policy{Write: []string{work}},
		TimeoutMS: extractionTimeout.Milliseconds(), MaxOutput: maxResponseBytes + 8192,
	}
	if format == "pdf" {
		node, nodeErr := executable(config.NodePath)
		if nodeErr != nil {
			return nodeErr
		}
		runtime, runtimeErr := readerRuntimeDirectory(config)
		if runtimeErr != nil {
			return runtimeErr
		}
		spec.Argv = []string{node, "--max-old-space-size=128", "--max-semi-space-size=8", filepath.Join(runtime, "extract.mjs")}
		spec.Policy.Read, spec.Policy.Executable = []string{runtime, node}, []string{node}
	} else {
		program, programErr := executable(config.ExecutablePath)
		if programErr != nil {
			return programErr
		}
		spec.Argv, spec.Policy.Read, spec.Policy.Executable = []string{program, "document-worker"}, []string{program}, []string{program}
	}
	// nativejob verifies kernel-observed descendants, including detached children.
	// The parser sees no control port, owner receipt, Server data or model state.
	result, err := nativejob.Run(ctx, helper, filepath.Join(operation, "jobs"), spec, input, stdout, stderr)
	if err != nil || !result.Clean {
		clean = false
		return errReaderOwnership
	}
	if result.Truncated {
		return failure("limit_exceeded", "The document reader exceeded its output limit.", false)
	}
	if result.Code != 0 {
		return errors.New("Document reader did not exit normally")
	}
	return nil
}
