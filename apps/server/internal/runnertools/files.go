package runnertools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
)

const (
	maxToolTextBytes = 1 << 20
	maxToolReadBytes = 8 << 20
)

type fileTarget struct {
	Key  string `json:"targetKey"`
	Path string `json:"displayPath"`
}

type fileIntent struct {
	Kind    string `json:"kind,omitempty"`
	Version string `json:"version,omitempty"`
}

type fileEdit struct {
	OldString  string `json:"oldString"`
	NewString  string `json:"newString"`
	ReplaceAll bool   `json:"replaceAll"`
}

type fileRequest struct {
	Operation string      `json:"operation"`
	Path      string      `json:"path,omitempty"`
	Cwd       string      `json:"cwd,omitempty"`
	Target    *fileTarget `json:"target,omitempty"`
	Content   *string     `json:"content,omitempty"`
	Expected  *fileIntent `json:"expected,omitempty"`
	Edit      *fileEdit   `json:"edit,omitempty"`
	MaxBytes  *int64      `json:"maxBytes,omitempty"`
	Offset    *int64      `json:"offset,omitempty"`
	Length    *int64      `json:"length,omitempty"`
}

type fileMetadata struct {
	Version string `json:"version"`
	Type    string `json:"type"`
	Size    int64  `json:"size"`
}

type fileEntry struct {
	Name   string     `json:"name"`
	Target fileTarget `json:"target"`
	fileMetadata
}

type fileWriteOutcome struct {
	Operation string  `json:"operation,omitempty"`
	Version   string  `json:"version"`
	Before    *string `json:"before"`
	After     string  `json:"after"`
}

func fileProblem(code, message string) error { return &toolError{Code: code, Message: message} }

func mapFileError(err error) error {
	if err == nil {
		return nil
	}
	var helper *BackendError
	if !errors.As(err, &helper) {
		return err
	}
	codes := map[string]string{
		"not_found": "FS_NOT_FOUND", "invalid_path": "FS_SANDBOX_DENIED", "invalid_root": "FS_SANDBOX_DENIED",
		"permission_denied": "FS_PERMISSION_DENIED", "read_only": "FS_PERMISSION_DENIED",
		"invalid_file_type": "FS_NOT_REGULAR_FILE", "unsupported_file": "FS_NOT_REGULAR_FILE",
		"file_too_large": "FS_TOO_LARGE", "version_conflict": "FS_STALE_VERSION", "already_exists": "FS_STALE_VERSION",
		"cancelled": "FS_ABORTED",
	}
	code := codes[helper.Code]
	if code == "" {
		code = "FS_IO_ERROR"
	}
	return fileProblem(code, helper.Message)
}

func fileErrorCode(err error, code string) bool {
	var known *toolError
	return errors.As(err, &known) && known.Code == code
}

func virtualFilePath(name, cwd string) (string, error) {
	if name == "" || len(name) > 4096 || strings.ContainsAny(name, "\\\x00") || !utf8.ValidString(name) {
		return "", fileProblem("FS_SANDBOX_DENIED", "Use a path under /workspace or /references.")
	}
	if cwd == "" {
		cwd = "/workspace"
	}
	if !allowedFilePath(cwd) {
		return "", fileProblem("FS_SANDBOX_DENIED", "The working directory is outside Camp files.")
	}
	if !path.IsAbs(name) {
		name = path.Join(cwd, name)
	} else {
		name = path.Clean(name)
	}
	if !allowedFilePath(name) {
		return "", fileProblem("FS_SANDBOX_DENIED", "The path is outside Camp files.")
	}
	return name, nil
}

func allowedFilePath(name string) bool {
	return utf8.ValidString(name) && !strings.ContainsAny(name, "\\\x00") && path.Clean(name) == name &&
		(name == "/workspace" || strings.HasPrefix(name, "/workspace/") || name == "/references" || strings.HasPrefix(name, "/references/"))
}

func (t *Tools) target(name string) fileTarget {
	digest := sha256.Sum256([]byte(t.token + "\x00" + name))
	return fileTarget{Key: hex.EncodeToString(digest[:]), Path: name}
}

func (t *Tools) targetPath(target *fileTarget) (string, error) {
	if target == nil || !allowedFilePath(target.Path) || len(target.Path) > 4096 {
		return "", fileProblem("FS_SANDBOX_DENIED", "The file target is not valid for this execution.")
	}
	expected := t.target(target.Path)
	if subtle.ConstantTimeCompare([]byte(target.Key), []byte(expected.Key)) != 1 {
		return "", fileProblem("FS_SANDBOX_DENIED", "The file target is not valid for this execution.")
	}
	return target.Path, nil
}

func (t *Tools) fileCall(ctx context.Context, request sandboxexec.Request) (sandboxexec.Frame, error) {
	frame, err := t.call(ctx, request, nil)
	return frame, mapFileError(err)
}

// fileOperation runs while the Run's file/command lease is held. File guards,
// literal matching and writes therefore cannot overlap another model tool.
func (t *Tools) fileOperation(ctx context.Context, input json.RawMessage) (any, error) {
	var request fileRequest
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nil, fileProblem("FS_IO_ERROR", "Provide a valid Camp file request.")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fileProblem("FS_IO_ERROR", "Provide exactly one Camp file request.")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.Operation == "resolve" || request.Operation == "lstat" {
		name, err := virtualFilePath(request.Path, request.Cwd)
		if err != nil {
			return nil, err
		}
		operation := "stat"
		if request.Operation == "lstat" {
			operation = "lstat"
		}
		frame, err := t.fileCall(ctx, sandboxexec.Request{Operation: operation, Path: name})
		if err != nil && !fileErrorCode(err, "FS_NOT_FOUND") {
			return nil, err
		}
		if request.Operation == "resolve" {
			return t.target(name), nil
		}
		if err != nil {
			return nil, nil
		}
		return metadataValue(frame.Info, true)
	}
	name, err := t.targetPath(request.Target)
	if err != nil {
		return nil, err
	}
	switch request.Operation {
	case "stat":
		frame, err := t.fileCall(ctx, sandboxexec.Request{Operation: "stat", Path: name})
		if fileErrorCode(err, "FS_NOT_FOUND") {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return metadataValue(frame.Info, false)
	case "listDir":
		frame, err := t.fileCall(ctx, sandboxexec.Request{Operation: "list", Path: name})
		if err != nil {
			if fileErrorCode(err, "FS_NOT_REGULAR_FILE") {
				err = fileProblem("FS_NOT_DIRECTORY", "List requires a directory.")
			}
			return nil, err
		}
		entries := make([]fileEntry, 0, len(frame.Entries))
		for _, entry := range frame.Entries {
			if !allowedFilePath(entry.Path) || path.Dir(entry.Path) != name || path.Base(entry.Path) != entry.Name {
				return nil, fileProblem("FS_IO_ERROR", "The file helper returned an invalid directory entry.")
			}
			metadata, err := metadataValue(&entry, false)
			if err != nil {
				return nil, err
			}
			entries = append(entries, fileEntry{Name: entry.Name, Target: t.target(entry.Path), fileMetadata: metadata})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		return entries, nil
	case "readText":
		data, err := t.readFile(ctx, name, maxToolTextBytes)
		if err != nil {
			return nil, err
		}
		return checkedText(data)
	case "readBytes":
		if request.MaxBytes == nil || *request.MaxBytes < 0 || *request.MaxBytes > maxToolReadBytes {
			return nil, fileProblem("FS_TOO_LARGE", "Read at most 8 MiB per file request.")
		}
		data, err := t.readFile(ctx, name, *request.MaxBytes)
		if err != nil {
			return nil, err
		}
		return base64.StdEncoding.EncodeToString(data), nil
	case "readByteRange":
		if request.Offset == nil || request.Length == nil || *request.Offset < 0 || *request.Length < 0 || *request.Length > maxToolReadBytes {
			return nil, fileProblem("FS_TOO_LARGE", "Use a nonnegative byte window of at most 8 MiB.")
		}
		frame, err := t.fileCall(ctx, sandboxexec.Request{Operation: "read", Path: name, Offset: *request.Offset, Length: request.Length})
		if err != nil {
			return nil, err
		}
		if int64(len(frame.Data)) > *request.Length {
			return nil, fileProblem("FS_IO_ERROR", "The file helper exceeded the requested byte window.")
		}
		return base64.StdEncoding.EncodeToString(frame.Data), nil
	case "writeText", "editText":
		if t.readOnly || name == "/references" || strings.HasPrefix(name, "/references/") {
			return nil, fileProblem("FS_PERMISSION_DENIED", "This file resource is read-only.")
		}
		if name == "/workspace" {
			return nil, fileProblem("FS_NOT_REGULAR_FILE", "The workspace root cannot be replaced.")
		}
		return t.changeText(ctx, name, request)
	default:
		return nil, fileProblem("FS_IO_ERROR", "This file operation is not supported.")
	}
}

func metadataValue(info *sandboxexec.FileInfo, symlink bool) (fileMetadata, error) {
	if info == nil || info.Size < 0 {
		return fileMetadata{}, fileProblem("FS_IO_ERROR", "The file helper returned invalid metadata.")
	}
	kind := info.Kind
	if kind != "file" && kind != "directory" && !(symlink && kind == "symlink") {
		kind = "other"
	}
	return fileMetadata{Version: info.Version, Type: kind, Size: info.Size}, nil
}

func (t *Tools) readFile(ctx context.Context, name string, limit int64) ([]byte, error) {
	frame, err := t.fileCall(ctx, sandboxexec.Request{Operation: "stat", Path: name})
	if err != nil {
		return nil, err
	}
	if frame.Info == nil || frame.Info.Kind != "file" {
		return nil, fileProblem("FS_NOT_REGULAR_FILE", "Read requires a regular file.")
	}
	if frame.Info.Size > limit {
		return nil, fileProblem("FS_TOO_LARGE", "The complete file exceeds the requested read limit.")
	}
	length := limit + 1
	frame, err = t.fileCall(ctx, sandboxexec.Request{Operation: "read", Path: name, Length: &length})
	if err != nil {
		return nil, err
	}
	if int64(len(frame.Data)) > limit || frame.Info == nil || frame.Info.Size > limit {
		return nil, fileProblem("FS_TOO_LARGE", "The complete file exceeds the requested read limit.")
	}
	return frame.Data, nil
}

func checkedText(data []byte) (string, error) {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", fileProblem("FS_NOT_TEXT", "The file is not valid UTF-8 text.")
	}
	return string(data), nil
}

func normalizeText(text string) string { return strings.ReplaceAll(text, "\r\n", "\n") }

func (t *Tools) changeText(ctx context.Context, name string, request fileRequest) (any, error) {
	editing := request.Operation == "editText"
	if editing {
		if request.Edit == nil || request.Edit.OldString == "" || request.Content != nil ||
			(request.Expected != nil && (request.Expected.Kind != "" || request.Expected.Version == "")) {
			return nil, fileProblem("FS_EDIT_NOT_FOUND", "Provide nonempty literal old text and replacement text.")
		}
	} else if request.Content == nil || request.Edit != nil || (request.Expected != nil && request.Expected.Kind != "createIfAbsent" && request.Expected.Kind != "replaceIfVersion") ||
		(request.Expected != nil && request.Expected.Kind == "replaceIfVersion" && request.Expected.Version == "") {
		return nil, fileProblem("FS_IO_ERROR", "Provide complete text and a valid optional write guard.")
	}
	frame, err := t.fileCall(ctx, sandboxexec.Request{Operation: "stat", Path: name})
	missing := fileErrorCode(err, "FS_NOT_FOUND")
	if err != nil && !missing {
		return nil, err
	}
	if !missing && (frame.Info == nil || frame.Info.Kind != "file") {
		return nil, fileProblem("FS_NOT_REGULAR_FILE", "Text changes require a regular file.")
	}
	if missing && (editing || (request.Expected != nil && request.Expected.Kind == "replaceIfVersion")) {
		return nil, fileProblem("FS_STALE_VERSION", "The file no longer exists; read it again.")
	}
	if !missing && request.Expected != nil {
		if request.Expected.Kind == "createIfAbsent" {
			return nil, fileProblem("FS_NOT_OBSERVED", "Read the existing file before replacing it.")
		}
		if request.Expected.Version != frame.Info.Version {
			return nil, fileProblem("FS_STALE_VERSION", "The file changed; read it again.")
		}
	}
	expected := "missing"
	var before *string
	var stored string
	if !missing {
		expected = frame.Info.Version
		data, readErr := t.readFile(ctx, name, maxToolTextBytes)
		if readErr == nil {
			stored, readErr = checkedText(data)
		}
		if readErr != nil {
			if editing || (!fileErrorCode(readErr, "FS_TOO_LARGE") && !fileErrorCode(readErr, "FS_NOT_TEXT")) {
				return nil, readErr
			}
		} else {
			basis := normalizeText(stored)
			before = &basis
		}
	}
	content := ""
	if editing {
		oldText, newText := normalizeText(request.Edit.OldString), normalizeText(request.Edit.NewString)
		count := strings.Count(*before, oldText)
		if count == 0 {
			return nil, fileProblem("FS_EDIT_NOT_FOUND", "The old text was not found in the file.")
		}
		if count > 1 && !request.Edit.ReplaceAll {
			return nil, fileProblem("FS_AMBIGUOUS_EDIT", "The old text matches more than once; use more context or replaceAll.")
		}
		content = strings.ReplaceAll(*before, oldText, newText)
		if strings.Contains(stored, "\r\n") && !strings.Contains(strings.ReplaceAll(stored, "\r\n", ""), "\n") {
			content = strings.ReplaceAll(content, "\n", "\r\n")
		}
	} else {
		content = *request.Content
	}
	if len(content) > maxToolTextBytes {
		return nil, fileProblem("FS_TOO_LARGE", "Text changes are limited to 1 MiB per file.")
	}
	if _, err := checkedText([]byte(content)); err != nil {
		return nil, err
	}
	if missing {
		if _, err := t.fileCall(ctx, sandboxexec.Request{Operation: "mkdir", Path: path.Dir(name), Recursive: true}); err != nil {
			return nil, err
		}
	}
	written, err := t.fileCall(ctx, sandboxexec.Request{Operation: "write", Path: name, Data: []byte(content), ExpectedVersion: expected})
	if err != nil {
		return nil, err
	}
	if written.Info == nil || written.Info.Version == "" {
		return nil, fileProblem("FS_IO_ERROR", "The file helper did not confirm the saved version.")
	}
	operation := ""
	if !editing {
		operation = "update"
		if missing {
			operation = "create"
		}
	}
	return fileWriteOutcome{Operation: operation, Version: written.Info.Version, Before: before, After: normalizeText(content)}, nil
}
