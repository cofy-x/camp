package resources

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

// MaxZIPBytes includes the existing content bound plus ZIP headers, names and
// compression overhead. It is not an additional allowance for project content.
const MaxZIPBytes = 2 * MaxProjectBytes

type zipOutput struct {
	writer    io.Writer
	remaining int
}

func (output *zipOutput) Write(data []byte) (int, error) {
	if len(data) > output.remaining {
		return 0, errors.New("The ZIP archive exceeds its size limit")
	}
	n, err := output.writer.Write(data)
	output.remaining -= n
	return n, err
}

// ExportZIP writes one immutable revision without a source directory or Git
// metadata. Callers must discard partial output after any error.
func (manager *Manager) ExportZIP(ctx context.Context, resourceID, commit string, output io.Writer) error {
	repository, err := manager.repository(resourceID)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(&zipOutput{writer: output, remaining: MaxZIPBytes})
	err = visitCommit(ctx, repository, commit, func(name string, entry treeEntry, content []byte) error {
		// Colons can name Windows drives or alternate data streams. The resource
		// layer already rejects traversal, absolute paths, backslashes and links.
		if strings.Contains(name, ":") {
			return errors.New("The ZIP archive contains a file name that cannot be safely extracted on every supported platform")
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		mode := os.FileMode(0644)
		if entry.executable {
			mode = 0755
		}
		header.SetMode(mode)
		file, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = file.Write(content)
		return err
	})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writer.Close()
}
