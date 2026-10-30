package nativeexecutor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"

	"github.com/cofy-x/camp/apps/server/internal/layerfs"
	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
)

func (e *layerRuntime) installSandbox(ctx context.Context, workspace layerfs.Workspace, references string) (*layerfs.Sandbox, error) {
	id, err := e.docker.Resolve(ctx, workspace.ContainerName)
	if err != nil {
		return nil, err
	}
	sandbox := &layerfs.Sandbox{Docker: e.docker, ContainerID: id, User: strconv.Itoa(workspace.UID) + ":" + strconv.Itoa(workspace.GID), Roots: sandboxexec.Roots{Workspace: workspace.MountPath, References: "/references"}}
	if err = sandbox.Setup(ctx, e.helper); err != nil {
		return sandbox, err
	}
	if references != "" {
		err = filepath.WalkDir(references, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			relative, err := filepath.Rel(references, name)
			if err != nil {
				return err
			}
			target := path.Join("/references", filepath.ToSlash(relative))
			if entry.IsDir() {
				code, err := e.docker.Exec(ctx, sandbox.ContainerID, "0:0", []string{"/bin/mkdir", "-p", "-m", "0555", "--", target}, io.Discard, io.Discard)
				if err != nil || code != 0 {
					return errors.Join(err, fmt.Errorf("Cannot install reference directory (exit %d)", code))
				}
				return nil
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
				return errors.New("Reference evidence must be a bounded regular file")
			}
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			return e.docker.InstallFile(ctx, sandbox.ContainerID, target, data, 0444)
		})
		if err != nil {
			return sandbox, err
		}
	}
	return sandbox, nil
}

// Dependency copies are trusted installer output, not model-created files.
// Preserve only relative links that still resolve within the owned project.
func copyDependencies(ctx context.Context, source, target, project string) error {
	var count int
	var total int64
	return filepath.WalkDir(source, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > 200000 {
			return errors.New("Prepared dependencies exceed the file limit")
		}
		relative, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(name)
			if err != nil {
				return err
			}
			resolved, err := filepath.Rel(project, filepath.Clean(filepath.Join(filepath.Dir(destination), link)))
			if err != nil || filepath.IsAbs(link) || !filepath.IsLocal(resolved) {
				return errors.New("Prepared dependency link escapes the project")
			}
			return os.Symlink(link, destination)
		}
		if !info.Mode().IsRegular() || info.Size() > 256<<20 {
			return errors.New("Prepared dependencies contain an unsupported file")
		}
		total += info.Size()
		if total > 2<<30 {
			return errors.New("Prepared dependencies exceed 2 GiB")
		}
		input, err := os.Open(name)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644|info.Mode().Perm()&0111)
		if err != nil {
			return err
		}
		_, err = io.Copy(output, input)
		return errors.Join(err, output.Close())
	})
}
