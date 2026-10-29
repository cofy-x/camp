// Package resources manages read-only project imports, controlled workspaces, and Git revisions.
package resources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

const (
	MaxFiles        = 10000
	MaxFileBytes    = 8 << 20
	MaxProjectBytes = 128 << 20
)

type file struct {
	content    []byte
	executable bool
	identity   os.FileInfo // Present only for host snapshots, never serialized.
}

type snapshot map[string]file

// Policy contains optional environment exclusions, separate from path safety.
type Policy struct {
	ExcludeNames []string
}

// Exclude host configuration and credentials; reject results containing these paths.
func excluded(name string) bool {
	name = strings.ToLower(name)
	return name == ".git" || name == ".env" || strings.HasPrefix(name, ".env.") ||
		name == ".ssh" || name == ".aws" || name == ".azure" || name == ".config" || name == ".gnupg" || name == ".docker" || name == ".kube" ||
		name == ".npmrc" || name == ".netrc" || name == ".pypirc" || name == ".envrc" || name == ".git-credentials" || name == "credentials" ||
		name == "credentials.json" || name == "secrets.json" || name == "id_rsa" || name == "id_ed25519" ||
		strings.HasSuffix(name, ".pem") || strings.HasSuffix(name, ".key") || strings.HasSuffix(name, ".p12") || strings.HasSuffix(name, ".pfx")
}

func validPath(name string) bool {
	if !validPathShape(name) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if excluded(part) {
			return false
		}
	}
	return true
}

func validPathShape(name string) bool {
	return name != "." && name != "" && len(name) <= 1024 && fs.ValidPath(name) && utf8.ValidString(name) && !strings.Contains(name, "\\") && strings.Count(name, "/") <= 20 && strings.IndexFunc(name, unicode.IsControl) < 0
}

// Read through os.Root and retain readable results when another file is unsafe.
func readSnapshot(ctx context.Context, directory string, importing bool, preserved ...snapshot) (snapshot, error) {
	return readSnapshotWithPolicy(ctx, directory, importing, Policy{}, preserved...)
}

func readSnapshotWithPolicy(ctx context.Context, directory string, importing bool, policy Policy, preserved ...snapshot) (snapshot, error) {
	initial, err := os.Lstat(directory)
	if err != nil || !initial.IsDir() {
		return nil, errors.New("The project root must be a directory, not a symbolic link")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("Cannot open the project directory: %w", err)
	}
	defer root.Close()
	current, err := root.Stat(".")
	if err != nil || !os.SameFile(initial, current) {
		return nil, errors.New("The project root changed before it could be read")
	}
	result := snapshot{}
	ignored, err := ignoredPaths(ctx, directory)
	if err != nil {
		return nil, err
	}
	var issues []error
	total, visited := 0, 0
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			issues = append(issues, fmt.Errorf("Cannot read project path %q", name))
			return fs.SkipDir
		}
		if name == "." {
			return nil
		}
		visited++
		if visited > 30000 {
			return errors.New("The project exceeds the limit of 30000 directory entries")
		}
		if slices.Contains(policy.ExcludeNames, path.Base(name)) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if excluded(path.Base(name)) {
			if !importing {
				issues = append(issues, fmt.Errorf("The result contains a prohibited path: %q", name))
			}
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		keep := false
		if len(preserved) > 0 {
			for saved := range preserved[0] {
				if saved == name || strings.HasPrefix(saved, name+"/") {
					keep = true
					break
				}
			}
		}
		if ignored[name] && !keep {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !validPath(name) {
			issues = append(issues, fmt.Errorf("The project path is not supported: %q", name))
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, statErr := root.Lstat(name)
		if statErr != nil {
			issues = append(issues, fmt.Errorf("Cannot inspect project path %q", name))
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			issues = append(issues, fmt.Errorf("Symbolic links and special files are not supported: %q", name))
			return nil
		}
		if len(result) >= MaxFiles || info.Size() > MaxFileBytes || int64(total)+info.Size() > MaxProjectBytes {
			issues = append(issues, errors.New("The project exceeds 10000 files, 8 MiB per file, or 128 MiB in total"))
			return fs.SkipAll
		}
		opened, openErr := openRegular(root, name)
		if openErr != nil {
			issues = append(issues, fmt.Errorf("Cannot read project file %q", name))
			return nil
		}
		current, statErr := opened.Stat()
		if statErr != nil || !os.SameFile(info, current) || !current.Mode().IsRegular() || !singleLink(current) {
			opened.Close()
			issues = append(issues, fmt.Errorf("The project file changed, has hard links, or cannot be read safely: %q", name))
			return nil
		}
		content, readErr := io.ReadAll(io.LimitReader(opened, MaxFileBytes+1))
		afterRead, afterErr := opened.Stat()
		afterPath, pathErr := root.Lstat(name)
		opened.Close()
		if afterErr != nil || pathErr != nil || !os.SameFile(current, afterRead) || !os.SameFile(current, afterPath) || current.Size() != afterRead.Size() || current.Mode() != afterRead.Mode() || !current.ModTime().Equal(afterRead.ModTime()) {
			issues = append(issues, fmt.Errorf("The project file changed during capture: %q", name))
			return nil
		}
		if readErr != nil || len(content) > MaxFileBytes || total+len(content) > MaxProjectBytes {
			issues = append(issues, fmt.Errorf("The project file cannot be read or exceeds the size limit: %q", name))
			return nil
		}
		result[name] = file{content: content, executable: current.Mode()&0111 != 0, identity: afterRead}
		total += len(content)
		return nil
	})
	if err != nil {
		issues = append(issues, err)
	}
	return result, errors.Join(issues...)
}

func differences(before, after snapshot) ([]storage.RunFile, error) {
	names := make([]string, 0, len(before)+len(after))
	for name := range before {
		names = append(names, name)
	}
	for name := range after {
		if _, exists := before[name]; !exists {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	result := []storage.RunFile{}
	for _, name := range names {
		previous, had := before[name]
		current, has := after[name]
		if had && has && string(previous.content) == string(current.content) && previous.executable == current.executable {
			continue
		}
		status := "modified"
		if !had {
			status = "added"
		} else if !has {
			status = "deleted"
		}
		item := storage.RunFile{Path: name, Status: status, Before: preview(previous.content), After: preview(current.content)}
		candidate := append(result, item)
		encoded, err := json.Marshal(candidate)
		if err != nil || len(encoded) > storage.MaxRunFilesSize {
			const omitted = "[Preview omitted; complete content is included in the patch]"
			if had {
				item.Before = omitted
			}
			if has {
				item.After = omitted
			}
			candidate = append(result, item)
			encoded, err = json.Marshal(candidate)
			if err != nil || len(encoded) > storage.MaxRunFilesSize {
				return result, errors.New("Changed file paths exceed 1 MiB; the displayable portion has been retained")
			}
		}
		result = candidate
	}
	return result, nil
}

func preview(content []byte) string {
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return fmt.Sprintf("[Binary file: %d bytes; complete content is included in the patch]", len(content))
	}
	const limit = 128 << 10
	if len(content) > limit {
		end := limit
		for !utf8.Valid(content[:end]) {
			end--
		}
		return string(content[:end]) + "\n[Preview truncated; complete content is included in the patch]"
	}
	return string(content)
}

// Git reads project ignore rules through a separate empty repository, so source
// hooks, filters, includes, and repository configuration never execute or load.
func ignoredPaths(ctx context.Context, directory string) (map[string]bool, error) {
	repository, err := os.MkdirTemp("", "camp-import-ignore-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(repository)
	if _, err := git(ctx, repository, nil, "init", "--bare", "--template=", repository); err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, "git", "--git-dir="+repository, "--work-tree="+directory, "-c", "core.excludesFile="+os.DevNull, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	command.Dir = directory
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=" + os.DevNull, "GIT_CONFIG_GLOBAL=" + os.DevNull}
	output := &limitedBuffer{limit: 8 << 20}
	command.Stdout = output
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("Cannot read project ignore rules: %w", err)
	}
	ignored := map[string]bool{}
	for _, name := range bytes.Split(output.Bytes(), []byte{0}) {
		if len(name) > 0 {
			ignored[strings.TrimSuffix(string(name), "/")] = true
		}
	}
	return ignored, nil
}

// CopySource copies safe regular files and honors source ignore rules.
func CopySource(ctx context.Context, source, destination string) error {
	return CopySourceWithPolicy(ctx, source, destination, Policy{})
}

func CopySourceWithPolicy(ctx context.Context, source, destination string, policy Policy) error {
	files, err := readSnapshotWithPolicy(ctx, source, true, policy)
	if err != nil {
		return err
	}
	return writeSnapshot(destination, files)
}

func writeSnapshot(directory string, files snapshot) error {
	if err := os.Mkdir(directory, 0700); err != nil {
		return fmt.Errorf("Cannot create the run directory: %w", err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for name, data := range files {
		if err := root.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return err
		}
		mode := fs.FileMode(0600)
		if data.executable {
			mode = 0700
		}
		opened, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		_, writeErr := opened.Write(data.content)
		closeErr := opened.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return err
		}
	}
	return nil
}
