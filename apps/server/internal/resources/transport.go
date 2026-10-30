package resources

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ExportArchive writes a deterministic archive from one immutable Git revision.
func (manager *Manager) ExportArchive(ctx context.Context, resourceID, commit string, output io.Writer) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	repository, err := manager.repository(resourceID)
	if err != nil {
		return err
	}
	files, err := loadCommit(ctx, repository, commit)
	if err != nil {
		return err
	}
	return writeArchive(ctx, files, output)
}

// ImportArchive validates all entries before making a new revision store visible.
func (manager *Manager) ImportArchive(ctx context.Context, resourceID string, input io.Reader) (ImportResult, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	repository, err := manager.repository(resourceID)
	if err != nil {
		return ImportResult{}, err
	}
	files, err := readArchive(ctx, input, Policy{})
	if err != nil {
		return ImportResult{}, err
	}
	if info, statErr := os.Lstat(repository); statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ImportResult{}, errors.New("The imported resource path is unsafe")
		}
		original, readErr := git(ctx, repository, nil, "rev-parse", "--verify", "refs/camp/remote-import")
		if readErr != nil {
			return ImportResult{}, errors.New("The resource already exists without an import receipt")
		}
		commit := strings.TrimSpace(string(original))
		previous, readErr := loadCommit(ctx, repository, commit)
		if readErr != nil || len(previous) != len(files) {
			return ImportResult{}, errors.New("The resource ID was reused for a different import")
		}
		for name, current := range files {
			old, ok := previous[name]
			if !ok || old.executable != current.executable || !bytes.Equal(old.content, current.content) {
				return ImportResult{}, errors.New("The resource ID was reused for a different import")
			}
		}
		return ImportResult{Commit: commit, Files: len(files)}, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return ImportResult{}, statErr
	}
	if err = os.MkdirAll(manager.root, 0700); err != nil {
		return ImportResult{}, err
	}
	temporary, err := os.MkdirTemp(manager.root, ".remote-import-")
	if err != nil {
		return ImportResult{}, err
	}
	defer os.RemoveAll(temporary)
	if _, err = git(ctx, temporary, nil, "init", "--bare", "--template=", "--object-format=sha1", "--initial-branch=main", temporary); err != nil {
		return ImportResult{}, err
	}
	commit, err := saveCommit(ctx, temporary, "", files)
	if err != nil {
		return ImportResult{}, err
	}
	if _, err = git(ctx, temporary, nil, "update-ref", "refs/camp/remote-import", commit); err != nil {
		return ImportResult{}, err
	}
	if err = os.Rename(temporary, repository); err != nil {
		return ImportResult{}, err
	}
	return ImportResult{Commit: commit, Files: len(files)}, nil
}

// WriteArchive packs a private, quiescent directory using the normal file policy.
// Unlike an import, prohibited files are rejected rather than silently omitted.
func WriteArchive(ctx context.Context, directory string, output io.Writer) error {
	return WriteArchiveWithPolicy(ctx, directory, output, Policy{})
}

// WriteArchiveWithPolicy excludes only explicitly prepared dependency names;
// the normal path, link, credential and size rejection rules still apply.
func WriteArchiveWithPolicy(ctx context.Context, directory string, output io.Writer, policy Policy) error {
	files, err := readSnapshotWithPolicy(ctx, directory, false, policy)
	if err != nil {
		return err
	}
	return writeArchive(ctx, files, output)
}

func writeArchive(ctx context.Context, files snapshot, output io.Writer) error {
	writer := tar.NewWriter(output)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		data := files[name]
		mode := int64(0644)
		if data.executable {
			mode = 0755
		}
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data.content)), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0), Format: tar.FormatPAX}); err != nil {
			return err
		}
		if _, err := writer.Write(data.content); err != nil {
			return err
		}
	}
	return writer.Close()
}

// ExtractArchive validates the complete archive before creating any files.
// destination must not exist; extraction never follows caller-controlled links.
func ExtractArchive(ctx context.Context, input io.Reader, destination string) error {
	files, err := readArchive(ctx, input, Policy{})
	if err != nil {
		return err
	}
	return writeSnapshot(destination, files)
}

// CaptureStableSource captures twice and compares bytes, modes and identities.
// The source is never modified. This detects observed changes, not filesystem
// snapshot atomicity; operators must quiesce a changing source for strict capture.
func CaptureStableSource(ctx context.Context, source, destination string) error {
	if !filepath.IsAbs(source) || filepath.Clean(source) != source {
		return errors.New("Source must be a normalized absolute directory")
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil || resolved != source {
		return errors.New("Source cannot contain symbolic links")
	}
	initial, err := os.Lstat(source)
	if err != nil || !initial.IsDir() {
		return errors.New("Source must be an existing directory")
	}
	first, err := readSnapshot(ctx, source, true)
	if err != nil {
		return err
	}
	second, err := readSnapshot(ctx, source, true)
	if err != nil {
		return err
	}
	final, statErr := os.Lstat(source)
	resolved, resolveErr := filepath.EvalSymlinks(source)
	if statErr != nil || resolveErr != nil || resolved != source || !os.SameFile(initial, final) || initial.Mode() != final.Mode() || !initial.ModTime().Equal(final.ModTime()) {
		return errors.New("Source directory changed during capture")
	}
	if err = verifySourceSnapshots(first, second); err != nil {
		return err
	}
	return writeSnapshot(destination, second)
}

func verifySourceSnapshots(first, second snapshot) error {
	if len(first) != len(second) {
		return errors.New("Source changed during capture")
	}
	for name, before := range first {
		after, ok := second[name]
		identity, current := before.identity, after.identity
		if !ok || identity == nil || current == nil || !os.SameFile(identity, current) || identity.Mode() != current.Mode() || identity.Size() != current.Size() || !identity.ModTime().Equal(current.ModTime()) || before.executable != after.executable || !bytes.Equal(before.content, after.content) {
			return errors.New("Source changed during capture")
		}
	}
	return nil
}
