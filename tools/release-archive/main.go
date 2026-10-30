// Command release-archive writes portable, reproducible release tarballs.
package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

func archive(directory, destination string) (err error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, output.Close())
		if err != nil {
			_ = os.Remove(destination)
		}
	}()
	compressed, err := gzip.NewWriterLevel(output, gzip.BestCompression)
	if err != nil {
		return err
	}
	writer := tar.NewWriter(compressed)
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := ""
		if info.Mode()&os.ModeSymlink != 0 {
			target, err = os.Readlink(name)
			if err != nil {
				return err
			}
			resolved, err := filepath.EvalSymlinks(name)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, resolved)
			if err != nil || filepath.IsAbs(target) || !filepath.IsLocal(relative) {
				return fmt.Errorf("release link escapes its tree: %s", name)
			}
		} else if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported release entry: %s", name)
		}
		header, err := tar.FileInfoHeader(info, target)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(filepath.Dir(root), name)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if info.IsDir() {
			header.Name += "/"
		}
		header.Uid, header.Gid, header.Uname, header.Gname = 0, 0, "", ""
		header.ModTime, header.AccessTime, header.ChangeTime = time.Unix(0, 0).UTC(), time.Time{}, time.Time{}
		header.Format = tar.FormatGNU
		header.Mode = 0644
		if info.IsDir() || info.Mode()&0111 != 0 {
			header.Mode = 0755
		}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			file, err := os.Open(name)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(writer, file)
			return errors.Join(copyErr, file.Close())
		}
		return nil
	})
	return errors.Join(err, writer.Close(), compressed.Close(), output.Sync())
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: release-archive <directory> <new-archive.tar.gz>")
		os.Exit(2)
	}
	if err := archive(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
