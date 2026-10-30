package nativeexecutor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

// Persist the immutable artifact before releasing the native workspace. This
// manifest is not a cleanup receipt: only the daemon's later journal transaction
// makes the operation eligible to resume upload after a crash.
func persistExport(archive workspace.Archive) error {
	file, err := os.Open(archive.ArchivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != archive.Bytes || archive.Bytes < 0 || archive.Bytes > 256<<20 {
		return errors.New("Native archive size is invalid")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != archive.SHA256 {
		return errors.New("Native archive checksum is invalid")
	}
	if err = file.Sync(); err != nil {
		return err
	}
	manifest, err := os.OpenFile(filepath.Join(filepath.Dir(archive.ArchivePath), "artifact.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(manifest).Encode(archive)
	if err == nil {
		err = manifest.Sync()
	}
	err = errors.Join(err, manifest.Close())
	if err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(archive.ArchivePath))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
