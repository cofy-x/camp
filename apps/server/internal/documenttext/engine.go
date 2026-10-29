package documenttext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const extractionTimeout = 30 * time.Second
const maxResponseBytes = 8 << 20

type Config struct {
	ExecutablePath string
	RuntimeDir     string
	NodePath       string
}

type Engine struct{ config Config }

func New(config Config) *Engine { return &Engine{config: config} }

// Extract does not write the original to disk or expose service credentials to
// the reader. A failed preparation never returns partially extracted content.
func (e *Engine) Extract(ctx context.Context, name string, content []byte) (Result, error) {
	format := Format(name)
	if format == "" {
		return Result{}, failure("unsupported_format", "Text preparation supports PDF and DOCX documents.", false)
	}
	if len(content) > MaxInputBytes {
		return Result{}, failure("input_limit", "The document exceeds the 8 MiB preparation limit.", false)
	}
	if len(content) == 0 {
		return Result{}, failure("malformed", "The document is empty and cannot be read.", false)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, contextFailure(err)
	}
	ctx, cancel := context.WithTimeout(ctx, extractionTimeout)
	defer cancel()
	config := e.config
	if config.ExecutablePath == "" {
		config.ExecutablePath, _ = os.Executable()
	}
	if config.NodePath == "" {
		config.NodePath, _ = exec.LookPath("node")
	}
	cmd, err := readerCommand(config, format)
	if err != nil {
		return Result{}, err
	}
	cmd.Stdin = bytes.NewReader(content)
	stdout := &boundedOutput{limit: maxResponseBytes, cancel: cancel}
	stderr := &boundedOutput{limit: 8192, discardOverflow: true}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Closing the pipes has a deadline too; a broken reader must not keep the
	// service waiting through a leaked descendant's inherited descriptor.
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return Result{}, failure("unavailable", "The isolated document reader could not start. Check Linux Bubblewrap and the document runtime.", true)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		// Bubblewrap owns a private PID namespace. Killing its supervisor also
		// removes detached descendants, not only the immediate child process.
		_ = cmd.Process.Kill()
		err = <-done
	}
	if stdout.overflow {
		return Result{}, failure("limit_exceeded", "The document reader exceeded its output limit.", false)
	}
	if ctx.Err() != nil {
		return Result{}, contextFailure(ctx.Err())
	}
	if err != nil {
		return Result{}, failure("reader_failed", "The document reader stopped before preparation completed. The original file is unchanged.", true)
	}
	var result response
	decoder := json.NewDecoder(bytes.NewReader(stdout.data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Result{}, failure("invalid_result", "The document reader returned an invalid response.", true)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) || (result.Error == nil) == (result.Result == nil) {
		return Result{}, failure("invalid_result", "The document reader returned an incomplete response.", true)
	}
	if result.Error != nil {
		if result.Error.Code == "" || result.Error.Message == "" {
			return Result{}, failure("invalid_result", "The document reader returned an invalid error.", true)
		}
		return Result{}, result.Error
	}
	if err := result.Result.validate(format); err != nil {
		return Result{}, err
	}
	return *result.Result, nil
}

func contextFailure(err error) *Failure {
	if errors.Is(err, context.DeadlineExceeded) {
		return &Failure{Code: "timeout", Message: "Document preparation exceeded its time limit (up to 30 seconds).", Retryable: true, cause: err}
	}
	return &Failure{Code: "cancelled", Message: "Document preparation was interrupted. Retry when the service is available.", Retryable: true, cause: err}
}

type boundedOutput struct {
	data            []byte
	limit           int
	overflow        bool
	discardOverflow bool
	cancel          context.CancelFunc
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := w.limit - len(w.data)
	if len(p) > remaining {
		w.overflow = true
		p = p[:remaining]
		if !w.discardOverflow && w.cancel != nil {
			w.cancel()
		}
	}
	w.data = append(w.data, p...)
	return n, nil
}

func executable(path string) (string, error) {
	if path == "" {
		return "", failure("unavailable", "A required document reader executable is missing.", true)
	}
	full, err := filepath.Abs(path)
	if err == nil {
		full, err = filepath.EvalSymlinks(full)
	}
	if err != nil {
		return "", failure("unavailable", "A required document reader executable is missing.", true)
	}
	info, err := os.Stat(full)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", failure("unavailable", "A required document reader executable cannot be started.", true)
	}
	return full, nil
}
