// Command release-archive writes portable, reproducible release tarballs.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

func writeTree(directory string, writer *tar.Writer) error {
	root, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
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
}

func archive(directory, destination string) (err error) {
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
	err = writeTree(directory, writer)
	return errors.Join(err, writer.Close(), compressed.Close(), output.Sync())
}

type countedWriter struct {
	io.Writer
	bytes int64
}

func (writer *countedWriter) Write(data []byte) (int, error) {
	n, err := writer.Writer.Write(data)
	writer.bytes += int64(n)
	return n, err
}

// Fingerprint the exact normalized tar stream, including every byte, mode,
// directory and symlink. Cache metadata never establishes artifact identity.
func treeFingerprint(directory string) ([32]byte, int64, error) {
	digest := sha256.New()
	counted := &countedWriter{Writer: digest}
	writer := tar.NewWriter(counted)
	err := errors.Join(writeTree(directory, writer), writer.Close())
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result, counted.bytes, err
}

func matchesArchive(filename string, expected [32]byte, size int64) bool {
	file, err := os.Open(filename)
	if err != nil {
		return false
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return false
	}
	defer reader.Close()
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(reader, size+1))
	var actual [32]byte
	copy(actual[:], digest.Sum(nil))
	return err == nil && n == size && actual == expected
}

func copyNew(source, destination string) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
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
	_, err = io.Copy(output, input)
	return errors.Join(err, output.Sync())
}

func cachedArchive(directory, destination, cacheDirectory string) (bool, error) {
	expected, size, err := treeFingerprint(directory)
	if err != nil {
		return false, err
	}
	cached := filepath.Join(cacheDirectory, "runtime.tar.gz")
	if matchesArchive(cached, expected, size) {
		return true, copyNew(cached, destination)
	}
	if err := archive(directory, destination); err != nil {
		return false, err
	}
	if !matchesArchive(destination, expected, size) {
		_ = os.Remove(destination)
		return false, errors.New("runtime tree changed while packaging")
	}
	if err := os.MkdirAll(cacheDirectory, 0755); err != nil {
		return false, err
	}
	staging, err := os.CreateTemp(cacheDirectory, ".runtime-*")
	if err != nil {
		return false, err
	}
	name := staging.Name()
	if err := staging.Close(); err != nil {
		return false, err
	}
	defer os.Remove(name)
	if err := os.Remove(name); err != nil {
		return false, err
	}
	if err := copyNew(destination, name); err != nil {
		return false, err
	}
	return false, os.Rename(name, cached)
}

func main() {
	cacheDirectory := flag.String("cache-dir", "", "optional verified runtime compression cache")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: release-archive [-cache-dir directory] <directory> <new-archive.tar.gz>")
		os.Exit(2)
	}
	var err error
	if *cacheDirectory == "" {
		err = archive(flag.Arg(0), flag.Arg(1))
	} else {
		var hit bool
		hit, err = cachedArchive(flag.Arg(0), flag.Arg(1), *cacheDirectory)
		if err == nil {
			fmt.Printf("Runtime archive compression cache hit=%t; complete normalized tree verified.\n", hit)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
