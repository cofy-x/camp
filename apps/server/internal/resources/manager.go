package resources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

var projectPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Manager struct {
	root string
	mu   sync.Mutex
}

type ImportResult struct {
	SourcePath string
	Commit     string
	Files      int
}

// New uses DATA_DIR/resources as its root; the parent directory is reserved for Camp data.
func New(root string) *Manager { return &Manager{root: root} }

// Import reads the selected directory without modifying it, including uncommitted changes in the initial revision.
func (manager *Manager) Import(ctx context.Context, projectID, sourcePath string) (ImportResult, error) {
	return manager.ImportWithPolicy(ctx, projectID, sourcePath, Policy{})
}

func (manager *Manager) ImportWithPolicy(ctx context.Context, projectID, sourcePath string, policy Policy) (ImportResult, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	repository, err := manager.repository(projectID)
	if err != nil {
		return ImportResult{}, err
	}
	if !filepath.IsAbs(sourcePath) || filepath.Clean(sourcePath) != sourcePath {
		return ImportResult{}, errors.New("The project source must be a normalized absolute directory path")
	}
	sourcePath = canonicalSystemPath(sourcePath)
	resolved, err := filepath.EvalSymlinks(sourcePath)
	if err != nil || resolved != sourcePath {
		return ImportResult{}, errors.New("The project source cannot contain symbolic links or missing directories")
	}
	managedRoot, err := filepath.Abs(manager.root)
	if err != nil {
		return ImportResult{}, errors.New("Cannot resolve the project storage path")
	}
	dataRoot := filepath.Dir(managedRoot)
	if resolvedData, err := filepath.EvalSymlinks(dataRoot); err == nil {
		dataRoot = resolvedData
		managedRoot = filepath.Join(dataRoot, filepath.Base(managedRoot))
	}
	containsStore := containsPath(sourcePath, managedRoot)
	if containsStore {
		relative, _ := filepath.Rel(sourcePath, managedRoot)
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			if excluded(part) || slices.Contains(policy.ExcludeNames, part) {
				containsStore = false
				break
			}
		}
	}
	if containsPath(dataRoot, sourcePath) || containsStore {
		return ImportResult{}, errors.New("The project source cannot be inside Camp data or contain the project revision store")
	}
	if _, err := os.Lstat(repository); !errors.Is(err, os.ErrNotExist) {
		return ImportResult{}, errors.New("The project workspace already exists or cannot be accessed")
	}
	files, err := readSnapshotWithPolicy(ctx, sourcePath, true, policy)
	if err != nil {
		return ImportResult{}, err
	}
	if err := os.MkdirAll(manager.root, 0700); err != nil {
		return ImportResult{}, fmt.Errorf("Cannot create project storage: %w", err)
	}
	temporary, err := os.MkdirTemp(manager.root, ".import-")
	if err != nil {
		return ImportResult{}, err
	}
	defer os.RemoveAll(temporary)
	if _, err := git(ctx, temporary, nil, "init", "--bare", "--template=", "--object-format=sha1", "--initial-branch=main", temporary); err != nil {
		return ImportResult{}, err
	}
	commit, err := saveCommit(ctx, temporary, "", files)
	if err != nil {
		return ImportResult{}, err
	}
	if err := os.Rename(temporary, repository); err != nil {
		return ImportResult{}, fmt.Errorf("Cannot save the project revision: %w", err)
	}
	return ImportResult{SourcePath: sourcePath, Commit: commit, Files: len(files)}, nil
}

// Prepare creates a fresh directory from a saved project revision without exposing the Git object store to the model.
func (manager *Manager) Prepare(ctx context.Context, projectID, commit, runDir string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	repository, err := manager.repository(projectID)
	if err != nil {
		return err
	}
	files, err := loadCommit(ctx, repository, commit)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(runDir, 0700); err != nil {
		return fmt.Errorf("Cannot create the run directory: %w", err)
	}
	return writeSnapshot(filepath.Join(runDir, "project"), files)
}

// Capture returns readable differences; unsafe or oversized results do not produce an acceptable revision.
func (manager *Manager) Capture(ctx context.Context, projectID, baseCommit, runDir string) (string, []storage.RunFile, error) {
	return manager.CaptureWithPolicy(ctx, projectID, baseCommit, runDir, Policy{})
}

func (manager *Manager) CaptureWithPolicy(ctx context.Context, projectID, baseCommit, runDir string, policy Policy) (string, []storage.RunFile, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	repository, err := manager.repository(projectID)
	if err != nil {
		return "", nil, err
	}
	before, err := loadCommit(ctx, repository, baseCommit)
	if err != nil {
		return "", nil, err
	}
	after, readErr := readSnapshotWithPolicy(ctx, filepath.Join(runDir, "project"), false, policy, before)
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

// ExportPatch creates a standard Git patch for download without modifying the source project.
func (manager *Manager) ExportPatch(ctx context.Context, projectID, baseCommit, resultCommit string) ([]byte, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	repository, err := manager.repository(projectID)
	if err != nil {
		return nil, err
	}
	if err := checkCommit(ctx, repository, baseCommit); err != nil {
		return nil, err
	}
	if err := checkCommit(ctx, repository, resultCommit); err != nil {
		return nil, err
	}
	return git(ctx, repository, nil, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--binary", "--full-index", baseCommit, resultCommit, "--")
}

func (manager *Manager) repository(projectID string) (string, error) {
	if !projectPattern.MatchString(projectID) {
		return "", errors.New("The project ID must contain 32 hexadecimal characters")
	}
	return filepath.Join(manager.root, projectID), nil
}

// DiscardImported removes an unregistered import after the caller fails to save its database record.
func (manager *Manager) DiscardImported(projectID string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, err := manager.repository(projectID); err != nil {
		return err
	}
	root, err := os.OpenRoot(manager.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("Cannot open project storage: %w", err)
	}
	defer root.Close()
	if err := root.RemoveAll(projectID); err != nil {
		return fmt.Errorf("Cannot remove the unregistered project revision: %w", err)
	}
	return nil
}

func containsPath(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
