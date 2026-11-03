// Package servicelog retains bounded, private logs for an explicitly started service.
package servicelog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const MaxBytes int64 = 10 << 20
const Files = 3

type Writer struct {
	mu    sync.Mutex
	path  string
	limit int64
	file  *os.File
	lock  *os.File
	size  int64
}

// Open never follows a linked log directory/file or accepts another owner's logs.
func Open(directory, name string) (*Writer, error) { return open(directory, name, MaxBytes) }

func open(directory, name string, limit int64) (*Writer, error) {
	if !filepath.IsAbs(directory) || filepath.Base(name) != name || name == "." || name == "" || limit <= 0 {
		return nil, errors.New("Invalid service log location")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || !owned(info) {
		return nil, errors.New("Service logs require a private owned directory")
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil || canonical != directory {
		return nil, errors.New("Service logs require a canonical directory")
	}
	w := &Writer{path: filepath.Join(directory, name), limit: limit}
	// The stable lock inode is separate from rotating payloads. Acquire it
	// before trimming or opening any log bytes, including failed/duplicate
	// service startup. Never unlink it while another opener can observe it.
	lock, err := lockPrivate(w.path + ".lock")
	if err != nil {
		return nil, err
	}
	w.lock = lock
	opened := false
	defer func() {
		if !opened {
			_ = lock.Close()
		}
	}()
	for i := 0; i < Files; i++ {
		name := w.filename(i)
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ownedSingle(info) {
			return nil, errors.New("Refusing an unowned, linked or shared service log")
		}
		if info.Size() > limit {
			if err := trim(name, info.Size()-limit); err != nil {
				return nil, err
			}
		}
	}
	if err := w.openFile(); err != nil {
		return nil, err
	}
	opened = true
	return w, nil
}

func trim(name string, offset int64) error {
	input, err := openPrivate(name, os.O_RDONLY)
	if err != nil {
		return err
	}
	defer input.Close()
	if _, err = input.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	output, err := os.CreateTemp(filepath.Dir(name), ".camp-log-")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	_, err = io.Copy(output, input)
	err = errors.Join(err, output.Sync(), output.Close())
	if err != nil {
		return err
	}
	return os.Rename(output.Name(), name)
}

func (w *Writer) filename(index int) string {
	if index == 0 {
		return w.path
	}
	return fmt.Sprintf("%s.%d", w.path, index)
}
func (w *Writer) openFile() error {
	file, err := openPrivate(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	w.file, w.size = file, info.Size()
	return nil
}
func (w *Writer) rotate() error {
	err := errors.Join(w.file.Sync(), w.file.Close())
	w.file = nil
	if err != nil {
		return err
	}
	for i := Files - 1; i >= 0; i-- {
		name := w.filename(i)
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || !ownedSingle(info) {
			return errors.New("Service log ownership changed")
		}
		if i == Files-1 {
			err = os.Remove(name)
		} else {
			err = os.Rename(name, w.filename(i+1))
		}
		if err != nil {
			return err
		}
	}
	return w.openFile()
}
func (w *Writer) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	count := 0
	for len(data) > 0 {
		if w.size == w.limit {
			if err := w.rotate(); err != nil {
				return count, err
			}
		}
		length := len(data)
		if int64(length) > w.limit-w.size {
			length = int(w.limit - w.size)
		}
		n, err := w.file.Write(data[:length])
		count += n
		w.size += int64(n)
		data = data[n:]
		if err != nil {
			return count, err
		}
		if n == 0 {
			return count, io.ErrShortWrite
		}
	}
	return count, nil
}
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var err error
	if w.file != nil {
		err = errors.Join(w.file.Sync(), w.file.Close())
		w.file = nil
	}
	// Failed rotation may already have closed the payload. It must not retain
	// stream ownership after the service has finished draining.
	if w.lock != nil {
		err = errors.Join(err, w.lock.Close())
		w.lock = nil
	}
	return err
}
