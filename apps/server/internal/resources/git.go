package resources

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	if len(data) > buffer.limit-buffer.Len() {
		return 0, errors.New("Git output exceeds the size limit")
	}
	return buffer.Buffer.Write(data)
}

// Use Git plumbing without user configuration, project filters, or hooks.
func git(ctx context.Context, repository string, input []byte, arguments ...string) ([]byte, error) {
	commandContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.attributesFile=" + os.DevNull,
		"-c", "core.fsmonitor=false", "-c", "protocol.file.allow=never", "--git-dir=" + repository}
	command := exec.CommandContext(commandContext, "git", append(args, arguments...)...)
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"), "LANG=C", "LC_ALL=C", "TZ=UTC", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=" + os.DevNull, "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=Camp", "GIT_AUTHOR_EMAIL=camp@localhost",
		"GIT_COMMITTER_NAME=Camp", "GIT_COMMITTER_EMAIL=camp@localhost",
	}
	command.Stdin = bytes.NewReader(input)
	command.WaitDelay = time.Second
	output := &limitedBuffer{limit: 2*MaxProjectBytes + (1 << 20)}
	command.Stdout = output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if commandContext.Err() != nil {
			return nil, fmt.Errorf("The Git operation was cancelled or timed out: %w", commandContext.Err())
		}
		return nil, fmt.Errorf("Git %s failed: %w", arguments[0], err)
	}
	return output.Bytes(), nil
}

func checkCommit(ctx context.Context, repository, commit string) error {
	if !commitPattern.MatchString(commit) {
		return errors.New("The project revision ID is invalid")
	}
	actual, err := git(ctx, repository, nil, "show-ref", "--verify", "--hash", "refs/camp/snapshots/"+commit)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if err != nil || strings.TrimSpace(string(actual)) != commit {
		return errors.New("The revision does not exist or does not belong to this project")
	}
	return nil
}

type treeEntry struct {
	object     string
	executable bool
}

// Read the index without loading every blob. Browsing or quoting one file must
// not spawn a Git process for every other file in a large resource.
func listCommit(ctx context.Context, repository, commit string) (map[string]treeEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if commit == EmptyRevision {
		if err := checkEmptyResource(repository); err != nil {
			return nil, err
		}
		return map[string]treeEntry{}, nil
	}
	if err := checkCommit(ctx, repository, commit); err != nil {
		return nil, err
	}
	listing, err := git(ctx, repository, nil, "ls-tree", "-r", "-z", commit)
	if err != nil {
		return nil, err
	}
	entries := map[string]treeEntry{}
	for _, record := range bytes.Split(listing, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		header, name, found := strings.Cut(string(record), "\t")
		fields := strings.Fields(header)
		if !found || len(fields) != 3 || (fields[0] != "100644" && fields[0] != "100755") || fields[1] != "blob" || !commitPattern.MatchString(fields[2]) || !validPath(name) {
			return nil, errors.New("The project revision contains unsupported file records")
		}
		if len(entries) >= MaxFiles {
			return nil, errors.New("The project revision exceeds the file count limit")
		}
		entries[name] = treeEntry{object: fields[2], executable: fields[0] == "100755"}
	}
	return entries, nil
}

func readBlob(ctx context.Context, repository, object string) ([]byte, error) {
	content, err := git(ctx, repository, nil, "cat-file", "blob", object)
	if err != nil {
		return nil, err
	}
	if len(content) > MaxFileBytes {
		return nil, errors.New("The project revision exceeds the file size limit")
	}
	return content, nil
}

func loadCommit(ctx context.Context, repository, commit string) (snapshot, error) {
	entries, err := listCommit(ctx, repository, commit)
	if err != nil {
		return nil, err
	}
	files := snapshot{}
	total := 0
	for name, entry := range entries {
		content, err := readBlob(ctx, repository, entry.object)
		if err != nil {
			return nil, err
		}
		total += len(content)
		if total > MaxProjectBytes {
			return nil, errors.New("The project revision exceeds the total file size limit")
		}
		files[name] = file{content: content, executable: entry.executable}
	}
	return files, nil
}

type tree struct {
	files       map[string]file
	directories map[string]*tree
}

func newTree() *tree { return &tree{files: map[string]file{}, directories: map[string]*tree{}} }

func saveCommit(ctx context.Context, repository, parent string, files snapshot) (string, error) {
	root := newTree()
	for name, content := range files {
		current := root
		parts := strings.Split(name, "/")
		for _, part := range parts[:len(parts)-1] {
			if current.directories[part] == nil {
				current.directories[part] = newTree()
			}
			current = current.directories[part]
		}
		current.files[path.Base(name)] = content
	}
	treeID, err := saveTree(ctx, repository, root)
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", treeID}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	result, err := git(ctx, repository, []byte("Camp project workspace\n"), args...)
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(result))
	if !commitPattern.MatchString(commit) {
		return "", errors.New("Git returned an invalid revision ID")
	}
	if _, err := git(ctx, repository, nil, "update-ref", "refs/camp/snapshots/"+commit, commit); err != nil {
		return "", err
	}
	return commit, nil
}

func saveTree(ctx context.Context, repository string, current *tree) (string, error) {
	names := make([]string, 0, len(current.files)+len(current.directories))
	for name := range current.files {
		names = append(names, name)
	}
	for name := range current.directories {
		names = append(names, name)
	}
	slices.Sort(names)
	var records bytes.Buffer
	for _, name := range names {
		if child := current.directories[name]; child != nil {
			id, err := saveTree(ctx, repository, child)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&records, "040000 tree %s\t%s\x00", id, name)
		} else {
			data := current.files[name]
			id, err := git(ctx, repository, data.content, "hash-object", "--no-filters", "-w", "--stdin")
			if err != nil {
				return "", err
			}
			mode := "100644"
			if data.executable {
				mode = "100755"
			}
			fmt.Fprintf(&records, "%s blob %s\t%s\x00", mode, strings.TrimSpace(string(id)), name)
		}
	}
	id, err := git(ctx, repository, records.Bytes(), "mktree", "-z")
	return strings.TrimSpace(string(id)), err
}
