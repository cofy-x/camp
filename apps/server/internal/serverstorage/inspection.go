// Package serverstorage owns stopped Server storage inspection and maintenance.
// Registered originals, versions, database records and Runner recovery never
// become orphan candidates merely because they are old or consume space.
package serverstorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/serviceowner"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

const MaxEntries = 100000
const MaxDatabaseBytes = int64(1 << 30)
const MaxObjectBytes = int64(512 << 20)
const MaxPlanObjects = 50

var objectID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var planID = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Object struct {
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes"`
	Files     int    `json:"files"`
	Retention string `json:"retention"`
}
type Report struct {
	Version         int                       `json:"version"`
	Identity        string                    `json:"identity"`
	Complete        bool                      `json:"complete"`
	ReferenceDigest string                    `json:"referenceDigest"`
	Bytes           int64                     `json:"bytes"`
	Files           int                       `json:"files"`
	Entries         int                       `json:"entries"`
	Objects         []Object                  `json:"objects"`
	Catalog         storage.InspectionCatalog `json:"catalog"`
	Verified        bool                      `json:"verified"`
}
type session struct {
	root    *os.Root
	owner   *serviceowner.Ownership
	catalog storage.InspectionCatalog
	digest  string
	stamps  map[string]os.FileInfo
}

func open(ctx context.Context, directory string) (s *session, err error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	owner, err := serviceowner.AcquireExisting(absolute)
	if err != nil {
		return nil, fmt.Errorf("Stop the Server before inspecting its data directory: %w", err)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		owner.Close()
		return nil, err
	}
	s = &session{root: root, owner: owner, stamps: map[string]os.FileInfo{}}
	defer func(opened *session) {
		if err != nil {
			opened.close()
		}
	}(s)
	// SQLite may need WAL/shm bookkeeping even in read-only mode. Read a stable
	// private copy so inspecting a crash cannot change or checkpoint the original.
	temporary, err := os.MkdirTemp("", "camp-server-inspection-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	hash := sha256.New()
	var total int64
	for _, name := range []string{"desktop.sqlite", "desktop.sqlite-wal"} {
		info, e := root.Lstat(name)
		if errors.Is(e, os.ErrNotExist) && name != "desktop.sqlite" {
			continue
		}
		if e != nil {
			return nil, e
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("The Server database and WAL must be regular files")
		}
		s.stamps[name] = info
		total += info.Size()
		if total > MaxDatabaseBytes {
			return nil, errors.New("The Server database exceeds the 1 GiB stopped inspection budget")
		}
		source, e := root.Open(name)
		if e != nil {
			return nil, e
		}
		target, e := os.OpenFile(filepath.Join(temporary, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			source.Close()
			return nil, e
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00", name, info.Size())
		_, e = copyContext(ctx, io.MultiWriter(target, hash), source, info.Size())
		closeErr := errors.Join(source.Close(), target.Close())
		if e != nil || closeErr != nil {
			return nil, errors.Join(e, closeErr)
		}
	}
	if err = s.unchanged(); err != nil {
		return nil, err
	}
	s.digest = hex.EncodeToString(hash.Sum(nil))
	s.catalog, err = storage.ReadInspectionCatalog(ctx, filepath.Join(temporary, "desktop.sqlite"))
	if err != nil {
		return nil, err
	}
	if !objectID.MatchString(s.catalog.Identity) {
		return nil, errors.New("The Server storage identity is invalid")
	}
	for id := range s.catalog.Resources {
		if !objectID.MatchString(id) {
			return nil, errors.New("A stored Resource identity is invalid")
		}
	}
	return s, nil
}
func (s *session) close() { _ = s.root.Close(); _ = s.owner.Close() }
func (s *session) unchanged() error {
	for _, name := range []string{"desktop.sqlite", "desktop.sqlite-wal"} {
		current, err := s.root.Lstat(name)
		before, existed := s.stamps[name]
		if !existed && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !existed || !current.Mode().IsRegular() || !os.SameFile(before, current) || current.Size() != before.Size() || !current.ModTime().Equal(before.ModTime()) {
			return errors.New("The stopped database changed during inspection; no maintenance is authorized")
		}
	}
	return nil
}
func copyContext(ctx context.Context, out io.Writer, in io.Reader, limit int64) (int64, error) {
	buffer := make([]byte, 64<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := in.Read(buffer)
		total += int64(n)
		if total > limit {
			return total, errors.New("A file grew beyond its inspection budget")
		}
		if n > 0 {
			written, e := out.Write(buffer[:n])
			if e != nil {
				return total, e
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}
func (s *session) retention(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !objectID.MatchString(parts[1]) {
		return "protected"
	}
	switch parts[0] {
	case "resources":
		if _, ok := s.catalog.Resources[parts[1]]; ok || s.catalog.Reserved[parts[1]] {
			return "protected"
		}
		return "orphan"
	case "remote":
		status, known := s.catalog.Transfers[parts[1]]
		if !known {
			return "orphan"
		}
		if slices.Contains([]string{"finished", "succeeded", "failed", "cancelled", "interrupted"}, status) {
			return "terminal-transfer"
		}
	}
	return "protected"
}
func (s *session) inspect(ctx context.Context) (Report, error) {
	report := Report{Version: 1, Identity: s.catalog.Identity, Complete: false, ReferenceDigest: s.digest, Catalog: s.catalog, Objects: []Object{}}
	remaining := MaxEntries
	top, err := s.root.Open(".")
	if err != nil {
		return report, err
	}
	entries, err := top.ReadDir(remaining + 1)
	top.Close()
	if err != nil && err != io.EOF {
		return report, err
	}
	if len(entries) > remaining {
		return report, errors.New("The Server inventory exceeds its entry budget")
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for _, entry := range entries {
		paths := []string{entry.Name()}
		if entry.Name() == "resources" || entry.Name() == "remote" {
			info, e := s.root.Lstat(entry.Name())
			if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return report, errors.New("Resource and transfer roots must be real directories")
			}
			directory, e := s.root.Open(entry.Name())
			if e != nil {
				return report, e
			}
			children, e := directory.ReadDir(remaining + 1)
			directory.Close()
			if e != nil && e != io.EOF {
				return report, e
			}
			if len(children) > remaining {
				return report, errors.New("The Server inventory exceeds its entry budget")
			}
			paths = nil
			for _, child := range children {
				paths = append(paths, entry.Name()+"/"+child.Name())
			}
			slices.Sort(paths)
		}
		for _, path := range paths {
			object := Object{Path: path, Retention: s.retention(path)}
			err = walk(ctx, s.root, path, 0, &remaining, func(_ string, info os.FileInfo) error {
				if info.Mode().IsRegular() {
					object.Bytes += info.Size()
					object.Files++
				}
				return nil
			})
			report.Objects = append(report.Objects, object)
			report.Bytes += object.Bytes
			report.Files += object.Files
			if err != nil {
				return report, err
			}
		}
	}
	if err = s.unchanged(); err != nil {
		return report, err
	}
	for id := range s.catalog.Resources {
		info, e := s.root.Lstat("resources/" + id)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return report, errors.New("A registered Resource directory is missing or unsupported")
		}
	}
	report.Entries = MaxEntries - remaining
	report.Complete = true
	return report, nil
}
func walk(ctx context.Context, root *os.Root, path string, depth int, remaining *int, visit func(string, os.FileInfo) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	*remaining--
	if *remaining < 0 || depth > 64 {
		return errors.New("The Server inventory exceeds its entry/depth budget")
	}
	info, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return fmt.Errorf("Unsupported link or special file in Server inventory: %s", path)
	}
	if err = visit(path, info); err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	directory, err := root.Open(path)
	if err != nil {
		return err
	}
	entries, err := directory.ReadDir(*remaining + 1)
	directory.Close()
	if err != nil && err != io.EOF {
		return err
	}
	if len(entries) > *remaining {
		return errors.New("The Server inventory exceeds its entry budget")
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for _, entry := range entries {
		if err = walk(ctx, root, path+"/"+entry.Name(), depth+1, remaining, visit); err != nil {
			return err
		}
	}
	return nil
}
func Inspect(ctx context.Context, directory string, verify bool) (Report, error) {
	s, err := open(ctx, directory)
	if err != nil {
		return Report{}, err
	}
	defer s.close()
	report, err := s.inspect(ctx)
	if err != nil {
		return report, err
	}
	if verify {
		files := resources.New(filepath.Join(s.root.Name(), "resources"))
		ids := make([]string, 0, len(s.catalog.Resources))
		for id := range s.catalog.Resources {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			if err = files.VerifyRevisions(ctx, id, s.catalog.Resources[id]); err != nil {
				report.Complete = false
				return report, err
			}
		}
		report.Verified = true
	}
	return report, nil
}
func fingerprint(value any) (string, []byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), data, nil
}
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
