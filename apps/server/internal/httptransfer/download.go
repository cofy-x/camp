// Package httptransfer provides bounded streaming for authenticated downloads.
package httptransfer

import (
	"errors"
	"io"
	"net/http"
	"time"
)

// Package downloads may outlast the server's ordinary request timeout. Bound
// each write instead so a progressing download can finish, while a stalled
// client cannot keep the connection indefinitely. Keep writes small and do not
// expose ReaderFrom: ServeContent must not bypass deadline renewal via sendfile.
type downloadWriter struct {
	http.ResponseWriter
	control *http.ResponseController
	idle    time.Duration
}

func (w *downloadWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *downloadWriter) renewDeadline() error {
	err := w.control.SetWriteDeadline(time.Now().Add(w.idle))
	if errors.Is(err, http.ErrNotSupported) {
		return nil // In-memory response recorders have no network deadline.
	}
	return err
}

func (w *downloadWriter) Write(data []byte) (written int, err error) {
	for len(data) > 0 {
		if err = w.renewDeadline(); err != nil {
			return written, err
		}
		chunk := data[:min(len(data), 32<<10)]
		n, err := w.ResponseWriter.Write(chunk)
		written += n
		if err != nil {
			return written, err
		}
		if n != len(chunk) {
			return written, io.ErrShortWrite
		}
		data = data[n:]
	}
	return written, nil
}

// DownloadWriter renews the write deadline while a client makes progress.
func DownloadWriter(w http.ResponseWriter, idle time.Duration) (http.ResponseWriter, error) {
	download := &downloadWriter{ResponseWriter: w, control: http.NewResponseController(w), idle: idle}
	if err := download.renewDeadline(); err != nil {
		return nil, err
	}
	return download, nil
}
