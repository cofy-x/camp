//go:build linux || darwin

package fsdurability

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

const concurrency = 8
const maxTreeEntries = 250_001 // The complete-tree manifest limit plus its root.
const maxPathBytes = 4096

// Tree supplies a durability boundary for an already closed private tree. It
// never follows a link, changes file bytes/modes, or validates snapshot content;
// the owning checkpoint adapter still performs independent manifest readback.
func Tree(ctx context.Context, directory string) error {
	return syncTree(ctx, directory, Entry, func(file *os.File) error { return file.Sync() })
}

func syncTree(ctx context.Context, directory string, entry func(*os.File) error, barrier func(*os.File) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	initial, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(directory) || !initial.IsDir() {
		return errors.New("staging synchronization requires an absolute directory, without a root symlink")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	current, err := root.Lstat(".")
	if err != nil || !os.SameFile(initial, current) {
		return errors.New("staging directory changed before synchronization")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type task struct {
		name string
		info fs.FileInfo
	}
	tasks := make(chan task, concurrency)
	var workers sync.WaitGroup
	var first error
	var once sync.Once
	fail := func(err error) {
		if err != nil {
			once.Do(func() { first = err; cancel() })
		}
	}
	for range concurrency {
		workers.Go(func() {
			for item := range tasks {
				if ctx.Err() != nil {
					continue
				}
				file, err := root.OpenFile(item.name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
				if err != nil {
					fail(err)
					continue
				}
				before, err := file.Stat()
				if err == nil && !sameEntry(item.info, before) {
					err = errors.New("staging entry changed before synchronization")
				}
				if err == nil {
					err = entry(file)
				}
				after, observed := file.Stat()
				if observed == nil && !sameEntry(item.info, after) {
					observed = errors.New("staging entry changed during synchronization")
				}
				fail(errors.Join(err, observed, file.Close()))
			}
		})
	}
	count := 0
	walkErr := fs.WalkDir(root.FS(), ".", func(name string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		count++
		if count > maxTreeEntries || len(name) > maxPathBytes {
			return errors.New("staging synchronization exceeds the checkpoint tree limits")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if item.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return errors.New("staging synchronization supports only regular files, directories and opaque links")
		}
		select {
		case tasks <- task{name, info}:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	fail(walkErr)
	close(tasks)
	workers.Wait() // Every admitted descriptor closes before failure or the barrier.
	if first != nil {
		return first
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err = os.Lstat(directory)
	if err != nil || !sameEntry(initial, current) {
		return errors.New("staging directory changed during synchronization")
	}
	stage, err := root.Open(".")
	if err != nil {
		return err
	}
	if err := errors.Join(barrier(stage), stage.Close()); err != nil {
		return err
	}
	current, err = os.Lstat(directory)
	if err != nil || !sameEntry(initial, current) {
		return errors.New("staging directory changed at its final barrier")
	}
	return ctx.Err()
}

func sameEntry(before, after fs.FileInfo) bool {
	return os.SameFile(before, after) && before.Mode() == after.Mode() && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}
