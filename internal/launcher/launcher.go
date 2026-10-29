// Package launcher installs immutable full Camp releases for the Go entrypoint.
// It never reads team configuration, credentials or Runner journals.
package launcher

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const maxArchive = 128 << 20
const maxUnpacked = 512 << 20

var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.(0|[1-9][0-9]*))?$`)

func ValidVersion(version string) bool { return versionPattern.MatchString(version) }

type Options struct {
	Version  string
	Role     string
	OS       string
	Arch     string
	CacheDir string
	Output   io.Writer
	// Tests substitute the transport/base without exposing download overrides in
	// the public command, environment or private team configuration.
	client *http.Client
	base   string
}

type receipt struct {
	Version string `json:"version"`
	Archive string `json:"archive"`
	SHA256  string `json:"binarySHA256"`
}

func (o Options) archive() (string, error) {
	if !ValidVersion(o.Version) {
		return "", errors.New("install a fixed Camp release with 'go install github.com/cofy-x/camp@<version>'; source development uses 'make build'")
	}
	if o.Role != "server" && o.Role != "runner" {
		return "", errors.New("unsupported Camp package role")
	}
	if o.Role == "runner" && (o.OS != "linux" || o.Arch != "amd64") {
		return "", errors.New("native Camp Runner requires Ubuntu 24.04 amd64; connect a Linux Runner from the browser")
	}
	if !(o.OS == "linux" && o.Arch == "amd64" || o.OS == "darwin" && (o.Arch == "amd64" || o.Arch == "arm64")) {
		return "", errors.New("Camp Server packages support Linux amd64 and macOS arm64/amd64")
	}
	return "camp-" + o.Role + "-" + o.OS + "-" + o.Arch + ".tar.gz", nil
}

// Ensure serializes installation, checks cached executable bytes on every use,
// and publishes only a completely verified extraction. Failed retries preserve
// both the prior installation and all independent Server/Runner data.
func Ensure(ctx context.Context, o Options) (string, error) {
	archive, err := o.archive()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(o.CacheDir) {
		return "", errors.New("Camp needs an absolute user cache directory")
	}
	if err := os.MkdirAll(o.CacheDir, 0700); err != nil {
		return "", fmt.Errorf("create user cache directory: %w", err)
	}
	directory := o.CacheDir
	for _, part := range []string{"camp", "releases", o.Version} {
		directory = filepath.Join(directory, part)
		if err := privateDirectory(directory); err != nil {
			return "", err
		}
	}
	name := strings.TrimSuffix(archive, ".tar.gz")
	lock := flock.New(filepath.Join(directory, name+".lock"))
	defer func() { _ = lock.Close() }()
	locked, err := lock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return "", fmt.Errorf("lock Camp installation: %w", err)
	}
	if !locked {
		return "", errors.New("Camp installation lock unavailable")
	}
	destination := filepath.Join(directory, name)
	if err := verifyCached(destination, o.Version, archive); err == nil {
		return filepath.Join(destination, "camp"), nil
	}
	if o.Output != nil {
		_, _ = fmt.Fprintf(o.Output, "Installing Camp %s (%s/%s, %s). Verifying the fixed GitHub release…\n", o.Version, o.OS, o.Arch, o.Role)
	}
	if err := install(ctx, o, directory, destination, archive); err != nil {
		return "", fmt.Errorf("install Camp %s: %w; retry the same command after fixing the problem (team and Runner data are unchanged)", o.Version, err)
	}
	return filepath.Join(destination, "camp"), nil
}

func privateDirectory(directory string) error {
	if err := os.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create private Camp cache: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("Camp cache must be a private directory, without symbolic links; choose a private user cache directory")
	}
	return nil
}

func verifyCached(directory, version, archive string) error {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("missing or unsafe cached installation")
	}
	for _, name := range []string{".camp-install.json", "camp"} {
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
			return errors.New("missing or unsafe cached file")
		}
		if name == "camp" && info.Mode().Perm()&0100 == 0 {
			return errors.New("cached binary is not executable")
		}
	}
	data, err := os.ReadFile(filepath.Join(directory, ".camp-install.json"))
	if err != nil || len(data) > 1024 {
		return errors.New("missing installation receipt")
	}
	var saved receipt
	if json.Unmarshal(data, &saved) != nil || saved.Version != version || saved.Archive != archive {
		return errors.New("installation receipt identity differs")
	}
	digest, err := hashFile(filepath.Join(directory, "camp"))
	if err != nil || digest != saved.SHA256 {
		return errors.New("cached executable digest differs")
	}
	return nil
}

func install(ctx context.Context, o Options, directory, destination, archive string) error {
	staging, err := os.MkdirTemp(directory, ".install-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	client := o.client
	if client == nil {
		client = publicClient()
	}
	base := o.base
	if base == "" {
		base = "https://github.com/cofy-x/camp/releases/download/" + o.Version + "/"
	}
	var checksum strings.Builder
	if err := download(ctx, client, base+archive+".sha256", &checksum, 256); err != nil {
		return fmt.Errorf("download checksum: %w", err)
	}
	fields := strings.Fields(checksum.String())
	if len(fields) != 2 || fields[1] != archive || len(fields[0]) != 64 {
		return errors.New("release checksum is missing or invalid")
	}
	if _, err := hex.DecodeString(fields[0]); err != nil {
		return errors.New("release checksum is invalid")
	}
	compressed := filepath.Join(staging, "archive.tar.gz")
	file, err := os.OpenFile(compressed, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	digest := sha256.New()
	err = download(ctx, client, base+archive, io.MultiWriter(file, digest), maxArchive)
	err = errors.Join(err, file.Close())
	if err != nil {
		return fmt.Errorf("download archive: %w", err)
	}
	if hex.EncodeToString(digest.Sum(nil)) != fields[0] {
		return errors.New("release archive checksum differs; no downloaded code was executed")
	}
	payload := filepath.Join(staging, "payload")
	if err := os.Mkdir(payload, 0700); err != nil {
		return err
	}
	if err := extract(ctx, compressed, payload, strings.TrimSuffix(archive, ".tar.gz")); err != nil {
		return err
	}
	binary := filepath.Join(payload, "camp")
	binaryDigest, err := hashFile(binary)
	if err != nil {
		return errors.New("release archive has no executable")
	}
	if err := os.Chmod(binary, 0700); err != nil {
		return err
	}
	data, _ := json.Marshal(receipt{Version: o.Version, Archive: archive, SHA256: binaryDigest})
	if err := os.WriteFile(filepath.Join(payload, ".camp-install.json"), data, 0600); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	old := filepath.Join(staging, "previous")
	if err := os.Rename(destination, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(payload, destination); err != nil {
		_ = os.Rename(old, destination)
		return err
	}
	return nil
}

func hashFile(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(file, maxUnpacked+1))
	if n > maxUnpacked {
		return "", errors.New("cached executable exceeds the size limit")
	}
	return hex.EncodeToString(digest.Sum(nil)), err
}

func publicClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		allowed := map[string]bool{"github.com": true, "release-assets.githubusercontent.com": true, "objects.githubusercontent.com": true, "github-releases.githubusercontent.com": true}
		if len(via) >= 5 || request.URL.Scheme != "https" || request.URL.User != nil || (request.URL.Port() != "" && request.URL.Port() != "443") || !allowed[request.URL.Hostname()] {
			return errors.New("release redirect rejected")
		}
		return nil
	}}
}

func download(ctx context.Context, client *http.Client, target string, output io.Writer, limit int64) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return errors.New("invalid release URL")
	}
	request.Header.Set("User-Agent", "Camp-release-launcher")
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Transport errors can contain signed redirect URLs or proxy credentials.
		return errors.New("GitHub release download failed; check network/TLS access and the fixed release's availability")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub release returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return errors.New("release download exceeds the size limit")
	}
	n, err := io.Copy(output, io.LimitReader(response.Body, limit+1))
	if n > limit {
		return errors.New("release download exceeds the size limit")
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("release transfer interrupted; retry the same fixed version")
	}
	return nil
}

func extract(ctx context.Context, filename, destination, prefix string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return errors.New("invalid release archive")
	}
	defer func() { _ = gz.Close() }()
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	reader := tar.NewReader(gz)
	seen := map[string]bool{}
	var total int64
	for entries := 0; ; entries++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || entries >= 10000 {
			return errors.New("invalid or excessive release archive entries")
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == prefix && header.Typeflag == tar.TypeDir {
			continue
		}
		if !strings.HasPrefix(name, prefix+"/") {
			return errors.New("release archive has an unexpected root")
		}
		name = strings.TrimPrefix(name, prefix+"/")
		if name == "" || path.Clean(name) != name || strings.Contains(name, "\\") || !filepath.IsLocal(name) || name == ".camp-install.json" || seen[name] {
			return errors.New("release archive has an unsafe or duplicate path")
		}
		seen[name] = true
		if header.Typeflag == tar.TypeDir {
			if err := root.MkdirAll(filepath.FromSlash(name), 0700); err != nil {
				return err
			}
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > maxUnpacked-total {
			return errors.New("release archive has an unsupported entry or exceeds the size limit")
		}
		total += header.Size
		if err := root.MkdirAll(filepath.FromSlash(path.Dir(name)), 0700); err != nil {
			return err
		}
		out, err := root.OpenFile(filepath.FromSlash(name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, reader)
		if err := errors.Join(copyErr, out.Close()); err != nil {
			return errors.New("release archive extraction interrupted")
		}
	}
	// Drain to validate the gzip footer/checksum, including truncated downloads.
	if n, err := io.Copy(io.Discard, io.LimitReader(gz, maxUnpacked+1)); err != nil || n > maxUnpacked {
		return errors.New("invalid compressed release archive")
	}
	return nil
}
