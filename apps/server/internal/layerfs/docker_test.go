package layerfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func dockerFrame(kind byte, data string) []byte {
	frame := make([]byte, 8+len(data))
	frame[0] = kind
	binary.BigEndian.PutUint32(frame[4:], uint32(len(data)))
	copy(frame[8:], data)
	return frame
}

func TestDockerFramesRejectTruncationAndInvalidHeaders(t *testing.T) {
	for _, frame := range [][]byte{{1}, {3, 0, 0, 0, 0, 0, 0, 0}, {1, 1, 0, 0, 0, 0, 0, 0}, {1, 0, 0, 0, 1, 0, 0, 1}, dockerFrame(1, "hello")[:10]} {
		if err := readDockerFrames(bytes.NewReader(frame), io.Discard, io.Discard); err == nil {
			t.Fatalf("Accepted corrupt frame %v", frame)
		}
	}
	var out, stderr bytes.Buffer
	frames := append(dockerFrame(1, "one"), dockerFrame(2, "two")...)
	if err := readDockerFrames(bytes.NewReader(frames), &out, &stderr); err != nil || out.String() != "one" || stderr.String() != "two" {
		t.Fatalf("Output framing: %q %q %v", out.String(), stderr.String(), err)
	}
}

func TestDockerStdinAndFrozenIdentity(t *testing.T) {
	id, execID := strings.Repeat("a", 64), strings.Repeat("b", 64)
	socket := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Repeat("large stdin\n", 24000)
	received := make(chan string, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case dockerAPIVersion + "/containers/camp-test/json":
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": id, "State": map[string]bool{"Running": true}})
		case dockerAPIVersion + "/containers/" + id + "/exec":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request["AttachStdin"] != true || request["User"] != "501:20" {
				t.Errorf("Unsafe exec request: %v, %v", request, err)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": execID})
		case dockerAPIVersion + "/exec/" + execID + "/start":
			_, _ = io.Copy(io.Discard, r.Body)
			conn, buffered, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			_, _ = buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			_ = buffered.Flush()
			data, err := io.ReadAll(buffered)
			if err != nil {
				t.Error(err)
			}
			received <- string(data)
			_, _ = conn.Write(dockerFrame(1, "confirmed"))
			_, _ = conn.Write(dockerFrame(2, "diagnostic"))
		case dockerAPIVersion + "/exec/" + execID + "/json":
			_ = json.NewEncoder(w).Encode(map[string]any{"Running": false, "ExitCode": 7})
		default:
			t.Errorf("Unexpected Docker path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	docker, err := NewDocker(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer docker.Close()
	resolved, err := docker.Resolve(context.Background(), "camp-test")
	if err != nil || resolved != id {
		t.Fatalf("Resolve: %s %v", resolved, err)
	}
	var out, stderr bytes.Buffer
	code, err := docker.ExecInput(context.Background(), id, "501:20", []string{"/camp-sandbox", "request"}, strings.NewReader(input), &out, &stderr)
	if err != nil || code != 7 || out.String() != "confirmed" || stderr.String() != "diagnostic" || <-received != input {
		t.Fatalf("Exec: code=%d out=%q stderr=%q err=%v", code, out.String(), stderr.String(), err)
	}
	if _, err := docker.Exec(context.Background(), "camp-test", "501:20", []string{"true"}, nil, nil); err == nil {
		t.Fatal("Container name accepted after identity freeze")
	}
}
