package resources

import (
	"bufio"
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
	"strconv"
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
func gitCommand(ctx context.Context, repository string, input []byte, arguments ...string) (*exec.Cmd, context.Context, context.CancelFunc) {
	commandContext, cancel := context.WithTimeout(ctx, 10*time.Second)
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
	return command, commandContext, cancel
}

func git(ctx context.Context, repository string, input []byte, arguments ...string) ([]byte, error) {
	command, commandContext, cancel := gitCommand(ctx, repository, input, arguments...)
	defer cancel()
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

// visitCommit uses one isolated Git process for all immutable blob reads. The
// caller receives one whole bounded file at a time, in deterministic path order.
func visitCommit(ctx context.Context, repository, commit string, visit func(string, treeEntry, []byte) error) error {
	entries, err := listCommit(ctx, repository, commit)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)
	var requests strings.Builder
	for _, name := range names {
		requests.WriteString(entries[name].object + "\n")
	}
	command, commandContext, cancel := gitCommand(ctx, repository, []byte(requests.String()), "cat-file", "--batch")
	defer cancel()
	output, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	defer output.Close()
	if err = command.Start(); err != nil {
		return fmt.Errorf("Git cat-file failed: %w", err)
	}
	readErr := readBlobBatch(ctx, bufio.NewReaderSize(output, 4096), names, entries, visit)
	if readErr != nil {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if commandContext.Err() != nil {
		return fmt.Errorf("The Git operation was cancelled or timed out: %w", commandContext.Err())
	}
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		return fmt.Errorf("Git cat-file failed: %w", waitErr)
	}
	return nil
}

func readBlobBatch(ctx context.Context, reader *bufio.Reader, names []string, entries map[string]treeEntry, visit func(string, treeEntry, []byte) error) error {
	total := int64(0)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.ReadSlice('\n')
		if err != nil || len(header) > 128 {
			return errors.New("The project revision contains an invalid or missing blob response")
		}
		fields := strings.Fields(string(header))
		entry := entries[name]
		if len(fields) != 3 || fields[0] != entry.object || fields[1] != "blob" {
			return errors.New("The project revision returned a different or unsupported Git object")
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 || size > MaxFileBytes {
			return errors.New("The project revision exceeds the file size limit")
		}
		total += size
		if total > MaxProjectBytes {
			return errors.New("The project revision exceeds the total file size limit")
		}
		content := make([]byte, int(size))
		if _, err = io.ReadFull(reader, content); err != nil {
			return errors.New("A project revision blob is truncated")
		}
		delimiter, err := reader.ReadByte()
		if err != nil || delimiter != '\n' {
			return errors.New("The project revision contains an invalid blob boundary")
		}
		if err = visit(name, entry, content); err != nil {
			return err
		}
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		return errors.New("The project revision returned unexpected blob data")
	}
	return nil
}

func loadCommit(ctx context.Context, repository, commit string) (snapshot, error) {
	files := snapshot{}
	err := visitCommit(ctx, repository, commit, func(name string, entry treeEntry, content []byte) error {
		files[name] = file{content: content, executable: entry.executable}
		return nil
	})
	return files, err
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
