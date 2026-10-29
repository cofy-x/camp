package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DownloadRuntime fetches one installer-pinned artifact from the enrolled Camp
// origin. A partial transfer can resume; the caller verifies the complete hash
// before extracting or executing any bytes.
func DownloadRuntime(ctx context.Context, config Config, digest string, size int64, file *os.File, progress func(int64)) error {
	if len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" || size <= 0 {
		return errors.New("Invalid pinned runtime download")
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	offset := info.Size()
	if offset > size {
		if err := file.Truncate(0); err != nil {
			return err
		}
		offset = 0
	}
	if offset == size {
		return nil
	}
	client, err := httpClient(config)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	target, err := endpoint(config.ServerURL, "/api/daemon/runtime/"+digest)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+config.Credential)
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return &httpError{Status: response.StatusCode}
	}
	if response.StatusCode == http.StatusPartialContent {
		if response.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", offset, size-1, size) {
			return errors.New("The runtime download returned an unexpected byte range")
		}
	} else if offset != 0 {
		if err := file.Truncate(0); err != nil {
			return err
		}
		offset = 0
	}
	if response.ContentLength >= 0 && response.ContentLength != size-offset {
		return errors.New("The runtime download has an unexpected size")
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	idle := time.AfterFunc(time.Minute, cancel)
	defer idle.Stop()
	buffer := make([]byte, 128<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			idle.Reset(time.Minute)
			if int64(n) > size-offset {
				return errors.New("The runtime download exceeds its pinned size")
			}
			written, writeErr := file.Write(buffer[:n])
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
			offset += int64(n)
			if progress != nil {
				progress(offset)
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return readErr
			}
			if offset != size {
				return io.ErrUnexpectedEOF
			}
			return file.Sync()
		}
	}
}
