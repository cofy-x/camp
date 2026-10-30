//go:build linux || darwin

package sandboxexec

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type rootedPath struct {
	root    *os.File
	rel     string
	virtual string
}

func (p rootedPath) close() { _ = p.root.Close() }

func checkedPath(roots Roots, name string, write bool) (rootedPath, error) {
	var result rootedPath
	if len(name) == 0 || len(name) > 4096 || strings.ContainsAny(name, "\\\x00") || !utf8.ValidString(name) || !strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return result, problem("invalid_path", "Use a normalized absolute path under /workspace or /references.")
	}
	root, prefix := roots.Workspace, "/workspace"
	if name == "/references" || strings.HasPrefix(name, "/references/") {
		root, prefix = roots.References, "/references"
		if write {
			return result, problem("read_only", "References are read-only.")
		}
	} else if name != prefix && !strings.HasPrefix(name, prefix+"/") {
		return result, problem("invalid_path", "The path is outside the allowed roots.")
	}
	if root == "" || root == "/" || !strings.HasPrefix(root, "/") || path.Clean(root) != root || strings.ContainsAny(root, "\\\x00") {
		return result, problem("invalid_root", "The trusted file root is unavailable.")
	}
	fd, err := openRoot(root)
	if err != nil {
		return result, fileError(err)
	}
	rel := strings.TrimPrefix(name, prefix)
	if rel == "" {
		rel = "."
	} else {
		rel = strings.TrimPrefix(rel, "/")
	}
	result = rootedPath{root: os.NewFile(uintptr(fd), root), rel: rel, virtual: name}
	return result, nil
}

func metadata(file *os.File, name string) (FileInfo, error) {
	stat, err := file.Stat()
	if err != nil {
		return FileInfo{}, fileError(err)
	}
	if stat.Mode().IsRegular() || stat.IsDir() {
		return fileMetadata(file, name, false)
	}
	kind := "other"
	if stat.Mode()&os.ModeSymlink != 0 {
		kind = "symlink"
	}
	return FileInfo{Path: name, Name: path.Base(name), Kind: kind, Size: stat.Size(), Mode: uint32(stat.Mode().Perm()), ModifiedAt: stat.ModTime().UTC().Format(time.RFC3339Nano)}, nil
}

func fileError(err error) error {
	if err == nil {
		return nil
	}
	var known *failure
	if errors.As(err, &known) {
		return err
	}
	switch {
	case errors.Is(err, unix.ENOENT):
		return problem("not_found", "The file or directory does not exist.")
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.EXDEV):
		return problem("invalid_path", "Symbolic links and path escapes are not allowed.")
	case errors.Is(err, unix.ENOTDIR), errors.Is(err, unix.EISDIR):
		return problem("invalid_file_type", "The path has an incompatible file type.")
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM), errors.Is(err, unix.EROFS):
		return problem("permission_denied", "The file operation is not permitted.")
	case errors.Is(err, unix.EEXIST):
		return problem("already_exists", "The destination already exists.")
	case errors.Is(err, unix.ENOSYS):
		return problem("unsupported_kernel", "Safe file access is unavailable on this kernel.")
	default:
		return problem("file_operation_failed", "The file operation failed.")
	}
}

func fileMetadata(file *os.File, name string, hash bool) (FileInfo, error) {
	stat, err := file.Stat()
	if err != nil {
		return FileInfo{}, fileError(err)
	}
	info := FileInfo{Path: name, Name: path.Base(name), Kind: "file", Size: stat.Size(), Mode: uint32(stat.Mode().Perm()), ModifiedAt: stat.ModTime().UTC().Format(time.RFC3339Nano)}
	if stat.IsDir() {
		info.Kind = "directory"
		info.Version = fmt.Sprintf("directory:%d", stat.ModTime().UnixNano())
		return info, nil
	}
	if !stat.Mode().IsRegular() {
		return FileInfo{}, problem("unsupported_file", "Only regular files and directories are supported.")
	}
	if hash {
		if stat.Size() > MaxFileBytes {
			return FileInfo{}, problem("file_too_large", "The file exceeds the 64 MiB helper limit.")
		}
		digest := sha256.New()
		count, err := io.Copy(digest, io.NewSectionReader(file, 0, MaxFileBytes+1))
		if err != nil {
			return FileInfo{}, fileError(err)
		}
		if count > MaxFileBytes {
			return FileInfo{}, problem("file_too_large", "The file exceeds the 64 MiB helper limit.")
		}
		info.Version = "sha256:" + hex.EncodeToString(digest.Sum(nil))
	}
	return info, nil
}

func files(ctx context.Context, request Request) (Frame, error) {
	write := request.Operation == "write" || request.Operation == "mkdir" || request.Operation == "remove" || request.Operation == "rename"
	root, err := checkedPath(request.Roots, request.Path, write)
	if err != nil {
		return Frame{}, err
	}
	defer root.close()
	if err := ctx.Err(); err != nil {
		return Frame{}, problem("cancelled", "The file operation was cancelled.")
	}
	switch request.Operation {
	case "read", "stat":
		return readFile(request, root)
	case "lstat":
		file, err := openMetadata(int(root.root.Fd()), root.rel)
		if err != nil {
			return Frame{}, err
		}
		defer file.Close()
		info, err := metadata(file, root.virtual)
		return Frame{Type: "result", Info: &info}, err
	case "list":
		entries := []FileInfo{}
		err := walk(ctx, root, request.Recursive, func(file *os.File, name string) error {
			info, err := metadata(file, name)
			if err == nil {
				entries = append(entries, info)
			}
			return err
		})
		return Frame{Type: "result", Entries: entries}, err
	case "write":
		return writeFile(request, root)
	case "mkdir":
		return makeDirectory(request, root)
	case "remove":
		return removePath(ctx, request, root)
	case "rename":
		return renamePath(request, root)
	case "search":
		return search(ctx, request, root)
	default:
		return Frame{}, problem("invalid_operation", "The requested file operation is not supported.")
	}
}

func readFile(request Request, root rootedPath) (Frame, error) {
	file, err := openRelative(int(root.root.Fd()), root.rel, unix.O_RDONLY)
	if err != nil {
		return Frame{}, err
	}
	defer file.Close()
	ranged := request.Offset != 0 || request.Length != nil
	info, err := fileMetadata(file, root.virtual, !ranged)
	if err != nil {
		return Frame{}, err
	}
	if request.Operation == "stat" {
		return Frame{Type: "result", Info: &info}, nil
	}
	if info.Kind != "file" {
		return Frame{}, problem("invalid_file_type", "Read requires a regular file.")
	}
	length := int64(MaxFileBytes)
	if request.Length != nil {
		length = *request.Length
	}
	if request.Offset < 0 || length < 0 || length > MaxFileBytes {
		return Frame{}, problem("invalid_range", "Use a nonnegative offset and a length no larger than 64 MiB.")
	}
	data, err := io.ReadAll(io.NewSectionReader(file, request.Offset, length))
	if err != nil {
		return Frame{}, fileError(err)
	}
	return Frame{Type: "result", Data: data, Info: &info}, nil
}

func parent(root rootedPath) (*os.File, string, error) {
	if root.rel == "." {
		return nil, "", problem("invalid_path", "The root directory cannot be replaced or removed.")
	}
	dir, err := openRelative(int(root.root.Fd()), path.Dir(root.rel), unix.O_RDONLY|unix.O_DIRECTORY)
	return dir, path.Base(root.rel), err
}

func writeFile(request Request, root rootedPath) (Frame, error) {
	if len(request.Data) > MaxFileBytes || request.Mode > 0777 {
		return Frame{}, problem("invalid_write", "Write at most 64 MiB with ordinary file permissions.")
	}
	dir, name, err := parent(root)
	if err != nil {
		return Frame{}, err
	}
	defer dir.Close()
	mode := uint32(0644)
	existing, openErr := openRelative(int(dir.Fd()), name, unix.O_RDONLY)
	if openErr == nil {
		info, statErr := fileMetadata(existing, root.virtual, request.ExpectedVersion != "")
		_ = existing.Close()
		if statErr != nil {
			return Frame{}, statErr
		}
		if info.Kind != "file" {
			return Frame{}, problem("invalid_file_type", "Write requires a regular file destination.")
		}
		if request.ExpectedVersion == "missing" || (request.ExpectedVersion != "" && request.ExpectedVersion != info.Version) {
			return Frame{}, problem("version_conflict", "The file changed; read it again before writing.")
		}
		mode = info.Mode
	} else {
		var failure *failure
		if !errors.As(openErr, &failure) || failure.code != "not_found" {
			return Frame{}, openErr
		}
		if request.ExpectedVersion != "" && request.ExpectedVersion != "missing" {
			return Frame{}, problem("version_conflict", "The file no longer exists.")
		}
	}
	if request.Mode != 0 {
		mode = request.Mode
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Frame{}, err
	}
	temporary := ".camp-write-" + hex.EncodeToString(random[:])
	fd, err := unix.Openat(int(dir.Fd()), temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return Frame{}, fileError(err)
	}
	defer unix.Unlinkat(int(dir.Fd()), temporary, 0)
	file := os.NewFile(uintptr(fd), temporary)
	_, err = file.Write(request.Data)
	if err == nil {
		err = file.Chmod(os.FileMode(mode))
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return Frame{}, fileError(err)
	}
	if err = renameAt(int(dir.Fd()), temporary, int(dir.Fd()), name, request.ExpectedVersion == "missing"); err != nil {
		if errors.Is(err, unix.EEXIST) && request.ExpectedVersion == "missing" {
			return Frame{}, problem("version_conflict", "The file already exists.")
		}
		return Frame{}, fileError(err)
	}
	written, err := openRelative(int(dir.Fd()), name, unix.O_RDONLY)
	if err != nil {
		return Frame{}, err
	}
	defer written.Close()
	info, err := fileMetadata(written, root.virtual, false)
	digest := sha256.Sum256(request.Data)
	info.Version = "sha256:" + hex.EncodeToString(digest[:])
	return Frame{Type: "result", Info: &info}, err
}

func makeDirectory(request Request, root rootedPath) (Frame, error) {
	if root.rel == "." {
		return Frame{Type: "result"}, nil
	}
	if request.Mode > 0777 {
		return Frame{}, problem("invalid_write", "Use ordinary directory permissions.")
	}
	mode := uint32(0755)
	if request.Mode != 0 {
		mode = request.Mode
	}
	if !request.Recursive {
		dir, name, err := parent(root)
		if err != nil {
			return Frame{}, err
		}
		defer dir.Close()
		return Frame{Type: "result"}, fileError(unix.Mkdirat(int(dir.Fd()), name, mode))
	}
	current := "."
	for _, part := range strings.Split(root.rel, "/") {
		current = path.Join(current, part)
		dir, err := openRelative(int(root.root.Fd()), path.Dir(current), unix.O_RDONLY|unix.O_DIRECTORY)
		if err != nil {
			return Frame{}, err
		}
		err = unix.Mkdirat(int(dir.Fd()), path.Base(current), mode)
		_ = dir.Close()
		if err != nil && !errors.Is(err, unix.EEXIST) {
			return Frame{}, fileError(err)
		}
		created, err := openRelative(int(root.root.Fd()), current, unix.O_RDONLY|unix.O_DIRECTORY)
		if err != nil {
			return Frame{}, err
		}
		_ = created.Close()
	}
	return Frame{Type: "result"}, nil
}

func removePath(ctx context.Context, request Request, root rootedPath) (Frame, error) {
	dir, name, err := parent(root)
	if err != nil {
		return Frame{}, err
	}
	defer dir.Close()
	visited := 0
	var remove func(*os.File, string, int) error
	remove = func(dir *os.File, name string, depth int) error {
		visited++
		if visited > MaxEntries || depth > 64 {
			return problem("operation_limit", "The directory exceeds the helper traversal limit.")
		}
		if ctx.Err() != nil {
			return problem("cancelled", "The file operation was cancelled.")
		}
		file, err := openRelative(int(dir.Fd()), name, unix.O_RDONLY)
		if err != nil {
			return err
		}
		defer file.Close()
		stat, err := file.Stat()
		if err != nil {
			return fileError(err)
		}
		flags := 0
		if stat.IsDir() {
			flags = unix.AT_REMOVEDIR
			if request.Recursive {
				names, err := file.Readdirnames(MaxEntries + 1)
				if err != nil && err != io.EOF {
					return fileError(err)
				}
				if len(names) > MaxEntries {
					return problem("operation_limit", "The directory exceeds the helper traversal limit.")
				}
				for _, child := range names {
					if err := remove(file, child, depth+1); err != nil {
						return err
					}
				}
			}
		}
		return fileError(unix.Unlinkat(int(dir.Fd()), name, flags))
	}
	return Frame{Type: "result"}, remove(dir, name, 0)
}

func renamePath(request Request, root rootedPath) (Frame, error) {
	target, err := checkedPath(request.Roots, request.Destination, true)
	if err != nil {
		return Frame{}, err
	}
	defer target.close()
	sourceFile, err := openRelative(int(root.root.Fd()), root.rel, pathOnly)
	if err != nil {
		return Frame{}, err
	}
	_ = sourceFile.Close()
	sourceDir, sourceName, err := parent(root)
	if err != nil {
		return Frame{}, err
	}
	defer sourceDir.Close()
	destinationDir, destinationName, err := parent(target)
	if err != nil {
		return Frame{}, err
	}
	defer destinationDir.Close()
	// Renames never overwrite an existing entry. A caller must explicitly remove
	// a destination before moving a file, rather than losing unrelated content.
	return Frame{Type: "result"}, fileError(renameAt(int(sourceDir.Fd()), sourceName, int(destinationDir.Fd()), destinationName, true))
}

func walk(ctx context.Context, root rootedPath, recursive bool, visit func(*os.File, string) error) error {
	visited := 0
	var descend func(string, string, int) error
	descend = func(relative, virtual string, depth int) error {
		if depth > 64 {
			return problem("operation_limit", "The directory exceeds the helper traversal limit.")
		}
		directory, err := openRelative(int(root.root.Fd()), relative, unix.O_RDONLY|unix.O_DIRECTORY)
		if err != nil {
			return err
		}
		names, err := directory.Readdirnames(MaxEntries + 1)
		_ = directory.Close()
		if err != nil && err != io.EOF {
			return fileError(err)
		}
		sort.Strings(names)
		for _, name := range names {
			visited++
			if visited > MaxEntries {
				return problem("operation_limit", "The directory exceeds the helper traversal limit.")
			}
			if ctx.Err() != nil {
				return problem("cancelled", "The file operation was cancelled.")
			}
			relativeName, virtualName := path.Join(relative, name), path.Join(virtual, name)
			file, err := openMetadata(int(root.root.Fd()), relativeName)
			if err != nil {
				return err
			}
			info, statErr := file.Stat()
			err = visit(file, virtualName)
			_ = file.Close()
			if err != nil {
				return err
			}
			if statErr != nil {
				return fileError(statErr)
			}
			if recursive && info.IsDir() {
				if err := descend(relativeName, virtualName, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return descend(root.rel, root.virtual, 0)
}

func search(ctx context.Context, request Request, root rootedPath) (Frame, error) {
	if request.Pattern == "" || len(request.Pattern) > 4096 || !utf8.ValidString(request.Pattern) {
		return Frame{}, problem("invalid_pattern", "Provide a nonempty UTF-8 literal search string of at most 4096 bytes.")
	}
	frame := Frame{Type: "result", Matches: []Match{}}
	scanned := int64(0)
	stopped := errors.New("search limit")
	err := walk(ctx, root, true, func(file *os.File, name string) error {
		info, err := file.Stat()
		if err != nil {
			return fileError(err)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > MaxFileBytes || scanned+info.Size() > MaxFileBytes {
			frame.Truncated = true
			return stopped
		}
		scanned += info.Size()
		readable, err := openRelative(int(root.root.Fd()), path.Join(root.rel, strings.TrimPrefix(name, root.virtual+"/")), unix.O_RDONLY)
		if err != nil {
			return err
		}
		defer readable.Close()
		reader := bufio.NewScanner(readable)
		reader.Buffer(make([]byte, 4096), 1<<20)
		line := 0
		for reader.Scan() {
			line++
			text := reader.Text()
			if strings.IndexByte(text, 0) >= 0 || !utf8.ValidString(text) {
				return nil
			}
			if strings.Contains(text, request.Pattern) {
				if len(frame.Matches) >= MaxMatches {
					frame.Truncated = true
					return stopped
				}
				if len(text) > 4096 {
					text = text[:4096]
					for !utf8.ValidString(text) {
						text = text[:len(text)-1]
					}
					frame.Truncated = true
				}
				frame.Matches = append(frame.Matches, Match{Path: name, Line: line, Text: text})
			}
		}
		if reader.Err() != nil {
			frame.Truncated = true
		}
		return nil
	})
	if errors.Is(err, stopped) {
		err = nil
	}
	return frame, err
}
