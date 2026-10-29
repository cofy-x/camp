package httptransfer

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

type downloadListener struct {
	connections chan net.Conn
	closed      chan struct{}
	once        sync.Once
	addr        net.Addr
}

func (l *downloadListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connections:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *downloadListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *downloadListener) Addr() net.Addr { return l.addr }

// net.Pipe applies write deadlines without kernel buffering hiding a slow or
// stalled client. The ordinary request timeout is intentionally much shorter
// than a progressing transfer, while the download idle timeout remains finite.
func downloadConnection(t *testing.T, handler http.Handler) net.Conn {
	t.Helper()
	serverConn, client := net.Pipe()
	listener := &downloadListener{connections: make(chan net.Conn, 1), closed: make(chan struct{}), addr: serverConn.LocalAddr()}
	listener.connections <- serverConn
	server := &http.Server{Handler: handler, WriteTimeout: 40 * time.Millisecond}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close(); <-done })
	if _, err := io.WriteString(client, "GET /package HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	return client
}

func TestRunnerDownloadRenewsDeadlineWhileClientProgresses(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 512<<10)
	client := downloadConnection(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		download := &downloadWriter{ResponseWriter: w, control: http.NewResponseController(w), idle: 200 * time.Millisecond}
		if err := download.renewDeadline(); err != nil {
			t.Error(err)
			return
		}
		http.ServeContent(download, r, "package.tar.gz", time.Time{}, bytes.NewReader(content))
	}))
	response, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result bytes.Buffer
	chunk := make([]byte, 16<<10)
	started := time.Now()
	for {
		n, err := response.Body.Read(chunk)
		result.Write(chunk[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal("progressing download was truncated", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if response.StatusCode != http.StatusOK || !bytes.Equal(result.Bytes(), content) || time.Since(started) <= 40*time.Millisecond {
		t.Fatal("download did not survive the ordinary request timeout", response.StatusCode, result.Len())
	}
}

func TestRunnerDownloadTimesOutStalledClient(t *testing.T) {
	done := make(chan error, 1)
	client := downloadConnection(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		download := &downloadWriter{ResponseWriter: w, control: http.NewResponseController(w), idle: 80 * time.Millisecond}
		_, err := download.Write(bytes.Repeat([]byte("x"), 64<<10))
		done <- err
	}))
	defer client.Close()
	select {
	case err := <-done:
		if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
			t.Fatalf("stalled write did not time out: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled client retained the download indefinitely")
	}
}
