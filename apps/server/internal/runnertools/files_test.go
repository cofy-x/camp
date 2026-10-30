package runnertools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
)

type fileFixture struct {
	files       map[string][]byte
	calls       []sandboxexec.Request
	beforeWrite func()
}

func fixtureVersion(content []byte) string {
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func (f *fileFixture) call(_ context.Context, request sandboxexec.Request, _ func(sandboxexec.Frame) error) (sandboxexec.Frame, error) {
	f.calls = append(f.calls, request)
	content, exists := f.files[request.Path]
	info := &sandboxexec.FileInfo{Path: request.Path, Name: path.Base(request.Path), Kind: "file", Size: int64(len(content)), Version: fixtureVersion(content)}
	if request.Path == "/workspace" || request.Path == "/references" {
		exists, info.Kind, info.Version = true, "directory", "directory"
	}
	switch request.Operation {
	case "stat", "lstat":
		if !exists {
			return sandboxexec.Frame{}, &BackendError{Code: "not_found", Message: "Missing file."}
		}
		return sandboxexec.Frame{Type: "result", Info: info}, nil
	case "read":
		if !exists {
			return sandboxexec.Frame{}, &BackendError{Code: "not_found", Message: "Missing file."}
		}
		start := min(request.Offset, int64(len(content)))
		end := int64(len(content))
		if request.Length != nil {
			end = min(end, start+*request.Length)
		}
		return sandboxexec.Frame{Type: "result", Info: info, Data: content[start:end]}, nil
	case "write":
		if f.beforeWrite != nil {
			f.beforeWrite()
		}
		current, present := f.files[request.Path]
		if (request.ExpectedVersion == "missing" && present) || (request.ExpectedVersion != "missing" && (!present || fixtureVersion(current) != request.ExpectedVersion)) {
			return sandboxexec.Frame{}, &BackendError{Code: "version_conflict", Message: "File changed."}
		}
		f.files[request.Path] = request.Data
		info.Version, info.Size = fixtureVersion(request.Data), int64(len(request.Data))
		return sandboxexec.Frame{Type: "result", Info: info}, nil
	case "mkdir":
		return sandboxexec.Frame{Type: "result"}, nil
	case "list":
		entries := []sandboxexec.FileInfo{}
		for name, data := range f.files {
			if path.Dir(name) == request.Path {
				entries = append(entries, sandboxexec.FileInfo{Path: name, Name: path.Base(name), Kind: "file", Size: int64(len(data))})
			}
		}
		return sandboxexec.Frame{Type: "result", Entries: entries}, nil
	default:
		return sandboxexec.Frame{}, errors.New("Unexpected helper request")
	}
}

func fixtureTools() (*Tools, *fileFixture) {
	fixture := &fileFixture{files: map[string][]byte{"/workspace/notes.txt": []byte("A note.\r\nA note.\r\n"), "/references/source.txt": []byte("Original evidence.")}}
	return &Tools{token: "private-run-token", call: fixture.call}, fixture
}

func invokeFile(t *testing.T, tools *Tools, request any) (any, error) {
	t.Helper()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return tools.fileOperation(context.Background(), data)
}

func TestFileTargetsBelongToExactlyOneRunAndRoot(t *testing.T) {
	tools, fixture := fixtureTools()
	value, err := invokeFile(t, tools, map[string]any{"operation": "resolve", "path": "./nested/../notes.txt"})
	if err != nil {
		t.Fatal(err)
	}
	target := value.(fileTarget)
	if target != tools.target("/workspace/notes.txt") || strings.Contains(target.Key, "private-run-token") {
		t.Fatalf("Invalid identity: %+v", target)
	}
	other := &Tools{token: "another-run"}
	for _, invalid := range []fileTarget{other.target(target.Path), {Key: target.Key, Path: "/etc/passwd"}, {Key: target.Key, Path: "/workspace/../references/source.txt"}} {
		previous := len(fixture.calls)
		_, err := invokeFile(t, tools, map[string]any{"operation": "readText", "target": invalid})
		if !fileErrorCode(err, "FS_SANDBOX_DENIED") || len(fixture.calls) != previous {
			t.Fatalf("Invalid target reached helper: %v", err)
		}
	}
	for _, name := range []string{"../../etc/passwd", "/state/session.json", "/workspace-other/file", "a\\b", "\x00"} {
		_, err := invokeFile(t, tools, map[string]any{"operation": "resolve", "path": name})
		if !fileErrorCode(err, "FS_SANDBOX_DENIED") {
			t.Fatalf("Path %q accepted: %v", name, err)
		}
	}
}

func TestReadonlyFilesRejectMutationsBeforeCallingHelper(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		tools, fixture := fixtureTools()
		tools.readOnly = readOnly
		name := "/references/source.txt"
		if readOnly {
			name = "/workspace/notes.txt"
		}
		for _, operation := range []string{"writeText", "editText"} {
			_, err := invokeFile(t, tools, map[string]any{"operation": operation, "target": tools.target(name), "content": "replacement"})
			if !fileErrorCode(err, "FS_PERMISSION_DENIED") || len(fixture.calls) != 0 {
				t.Fatalf("Readonly mutation reached helper: %v", err)
			}
		}
	}
}

func TestTextEditsPreserveCRLFAndEnforceLiteralGuards(t *testing.T) {
	tools, fixture := fixtureTools()
	target := tools.target("/workspace/notes.txt")
	request := map[string]any{"operation": "editText", "target": target, "edit": fileEdit{OldString: "A note.", NewString: "Updated."}}
	_, err := invokeFile(t, tools, request)
	if !fileErrorCode(err, "FS_AMBIGUOUS_EDIT") {
		t.Fatalf("Ambiguous edit: %v", err)
	}
	request["edit"] = fileEdit{OldString: "A note.", NewString: "Updated.", ReplaceAll: true}
	request["expected"] = fileIntent{Version: "stale"}
	_, err = invokeFile(t, tools, request)
	if !fileErrorCode(err, "FS_STALE_VERSION") {
		t.Fatalf("Stale edit: %v", err)
	}
	request["expected"] = fileIntent{Version: fixtureVersion(fixture.files[target.Path])}
	value, err := invokeFile(t, tools, request)
	if err != nil {
		t.Fatal(err)
	}
	outcome := value.(fileWriteOutcome)
	if outcome.After != "Updated.\nUpdated.\n" || *outcome.Before != "A note.\nA note.\n" || string(fixture.files[target.Path]) != "Updated.\r\nUpdated.\r\n" {
		t.Fatalf("Incorrect line ending handling: %+v", outcome)
	}
	last := fixture.calls[len(fixture.calls)-1]
	if last.ExpectedVersion == "" {
		t.Fatal("Edit lost its atomic helper version guard")
	}
}

func TestWriteCreatesParentsAndDoesNotOverwriteUnobservedOrRacedFiles(t *testing.T) {
	tools, fixture := fixtureTools()
	target := tools.target("/workspace/new/report.md")
	request := map[string]any{"operation": "writeText", "target": target, "content": "# Report\n", "expected": fileIntent{Kind: "createIfAbsent"}}
	value, err := invokeFile(t, tools, request)
	if err != nil {
		t.Fatal(err)
	}
	if value.(fileWriteOutcome).Operation != "create" {
		t.Fatal("Creation not reported")
	}
	if fixture.calls[len(fixture.calls)-2].Operation != "mkdir" || fixture.calls[len(fixture.calls)-1].ExpectedVersion != "missing" {
		t.Fatal("Missing parent creation or atomic create guard")
	}
	_, err = invokeFile(t, tools, request)
	if !fileErrorCode(err, "FS_NOT_OBSERVED") {
		t.Fatalf("Replaced an unobserved existing file: %v", err)
	}
	delete(request, "expected")
	fixture.beforeWrite = func() { fixture.files[target.Path] = []byte("Concurrent edit") }
	_, err = invokeFile(t, tools, request)
	if !fileErrorCode(err, "FS_STALE_VERSION") || string(fixture.files[target.Path]) != "Concurrent edit" {
		t.Fatalf("Concurrent edit overwritten: %v", err)
	}
}

func TestFileReadsRejectBinaryOversizeAndReturnExactByteWindows(t *testing.T) {
	tools, fixture := fixtureTools()
	fixture.files["/workspace/binary"] = []byte{0, 255, 1, 2}
	_, err := invokeFile(t, tools, map[string]any{"operation": "readText", "target": tools.target("/workspace/binary")})
	if !fileErrorCode(err, "FS_NOT_TEXT") {
		t.Fatalf("Binary accepted as text: %v", err)
	}
	_, err = invokeFile(t, tools, map[string]any{"operation": "readBytes", "target": tools.target("/workspace/binary"), "maxBytes": 3})
	if !fileErrorCode(err, "FS_TOO_LARGE") {
		t.Fatalf("Whole read silently truncated: %v", err)
	}
	value, err := invokeFile(t, tools, map[string]any{"operation": "readByteRange", "target": tools.target("/workspace/binary"), "offset": 1, "length": 2})
	if err != nil || value != "/wE=" {
		t.Fatalf("Invalid byte window: %v %v", value, err)
	}
	fixture.files["/workspace/large"] = []byte(strings.Repeat("x", maxToolTextBytes+1))
	prior := len(fixture.calls)
	_, err = invokeFile(t, tools, map[string]any{"operation": "readText", "target": tools.target("/workspace/large")})
	if !fileErrorCode(err, "FS_TOO_LARGE") || len(fixture.calls) != prior+1 {
		t.Fatalf("Oversized file was buffered: %v", err)
	}
}

func TestMissingMetadataListsAndMalformedRequests(t *testing.T) {
	tools, _ := fixtureTools()
	value, err := invokeFile(t, tools, map[string]any{"operation": "stat", "target": tools.target("/workspace/missing")})
	if err != nil || value != nil {
		t.Fatalf("Missing stat: %v %v", value, err)
	}
	value, err = invokeFile(t, tools, map[string]any{"operation": "resolve", "path": "/workspace/future/file.txt"})
	if err != nil || value != tools.target("/workspace/future/file.txt") {
		t.Fatalf("Missing target resolution: %v %v", value, err)
	}
	value, err = invokeFile(t, tools, map[string]any{"operation": "listDir", "target": tools.target("/workspace")})
	if err != nil || len(value.([]fileEntry)) != 1 {
		t.Fatalf("Directory list: %v %v", value, err)
	}
	for _, raw := range []string{`{"operation":"stat","unknown":true}`, `{} {}`, `[]`} {
		if _, err := tools.fileOperation(context.Background(), json.RawMessage(raw)); err == nil {
			t.Fatalf("Accepted malformed request: %s", raw)
		}
	}
}
