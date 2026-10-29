package resources

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// CaptureArchive captures a quiescent LayerFS export directly into the existing
// Git object store. Untrusted tar headers never become host filesystem paths.
// Publishing the returned commit remains the caller's SQLite transaction.
func (manager *Manager) CaptureArchive(ctx context.Context, projectID, baseCommit string, archive io.Reader, policy Policy) (string, []storage.RunFile, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	repository, err := manager.repository(projectID)
	if err != nil {
		return "", nil, err
	}
	before, err := loadCommit(ctx, repository, baseCommit)
	if err != nil {
		return "", nil, err
	}
	after, readErr := readArchive(ctx, archive, policy)
	files, diffErr := differences(before, after)
	if err := errors.Join(readErr, diffErr); err != nil {
		return "", files, err
	}
	if baseCommit == EmptyRevision {
		if err := materializeEmptyResource(ctx, repository); err != nil {
			return "", files, err
		}
	}
	commit, err := saveCommit(ctx, repository, baseCommit, after)
	return commit, files, err
}

func readArchive(ctx context.Context, archive io.Reader, policy Policy) (snapshot, error) {
	reader := tar.NewReader(archive)
	result, seen := snapshot{}, map[string]bool{}
	var total int64
	entries := 0
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		header, err := reader.Next()
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return result, fmt.Errorf("Cannot read the exported file archive: %w", err)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(header.Name, "./"), "/")
		if (name == "." || name == "") && header.Typeflag == tar.TypeDir {
			continue
		}
		if !validPathShape(name) {
			return result, fmt.Errorf("The result contains an unsupported or prohibited path: %q", name)
		}
		// Environment exclusions are explicit. Ordinary documents named dist or
		// coverage are never excluded by the language-neutral resource layer.
		ignored := false
		for _, part := range strings.Split(name, "/") {
			if slices.Contains(policy.ExcludeNames, part) {
				ignored = true
				break
			}
		}
		if ignored {
			continue
		}
		if !validPath(name) {
			return result, fmt.Errorf("The result contains a prohibited path: %q", name)
		}
		entries++
		if entries > 30000 || seen[name] {
			return result, errors.New("The result contains duplicate paths or more than 30000 directory entries")
		}
		seen[name] = true
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Linkname != "" || header.Size < 0 || header.Mode&07000 != 0 {
			return result, fmt.Errorf("The result contains a link, special file, or unsafe mode: %q", name)
		}
		if len(result) >= MaxFiles || header.Size > MaxFileBytes || total+header.Size > MaxProjectBytes {
			return result, errors.New("The result exceeds 10000 files, 8 MiB per file, or 128 MiB in total")
		}
		content, err := io.ReadAll(io.LimitReader(reader, MaxFileBytes+1))
		if err != nil || int64(len(content)) != header.Size {
			return result, errors.New("An exported result file is truncated")
		}
		result[name] = file{content: content, executable: header.Mode&0111 != 0}
		total += header.Size
	}
}
