package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The verified runtime selects the image. An archive cannot choose another
// runtime or cause a pull, and loading it never removes an existing resource.
type checkpointImagePin struct {
	Image         string `json:"image"`
	ArchiveSHA256 string `json:"archiveSHA256"`
	ArchiveBytes  int64  `json:"archiveBytes"`
	Filename      string `json:"filename"`
}

func (pin checkpointImagePin) valid() bool {
	return strings.HasPrefix(pin.Image, "sha256:") && digestPattern.MatchString(strings.TrimPrefix(pin.Image, "sha256:")) && digestPattern.MatchString(pin.ArchiveSHA256) && pin.ArchiveBytes > 0 && pin.ArchiveBytes <= 2<<30
}

func loadCheckpointImage(ctx context.Context, data, archive, docker, socket string, pin checkpointImagePin) error {
	if !pin.valid() {
		return errors.New("This runtime has no optional checkpoint worker pin; install a compatible Camp release")
	}
	image := pin.Image
	control, err := os.MkdirTemp(data, ".worker-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(control)
	if err := os.WriteFile(filepath.Join(control, "config.json"), []byte("{\"auths\":{}}\n"), 0600); err != nil {
		return err
	}
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, docker, args...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + control, "DOCKER_HOST=unix://" + socket, "DOCKER_CONFIG=" + control}
		var output bytes.Buffer
		cmd.Stdout, cmd.Stderr = &checkpointDiagnostic{writer: &output}, &checkpointDiagnostic{writer: &output}
		err := cmd.Run()
		return output.Bytes(), err
	}
	inspect := func() error {
		output, err := run("image", "inspect", image)
		if err != nil {
			return err
		}
		var records []struct {
			ID           string `json:"Id"`
			OS           string `json:"Os"`
			Architecture string `json:"Architecture"`
			Config       struct {
				Labels map[string]string `json:"Labels"`
			} `json:"Config"`
		}
		if json.Unmarshal(output, &records) != nil || len(records) != 1 || records[0].ID != image || records[0].OS != "linux" || records[0].Architecture != "arm64" || records[0].Config.Labels["camp.runtime"] != "layerfs-worker" {
			return errors.New("The loaded checkpoint image differs from the native ARM64 runtime pin")
		}
		return nil
	}
	if err := inspect(); err == nil {
		return nil
	}
	if archive == "" {
		return fmt.Errorf("The optional Linux ARM64 checkpoint image is missing. Download its matching Camp worker artifact and repeat stopped setup with --worker-image PATH (expected %s)", image)
	}
	archive, err = filepath.Abs(archive)
	if err != nil {
		return err
	}
	info, err := os.Lstat(archive)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != pin.ArchiveBytes {
		return errors.New("The worker image archive differs from the fixed size")
	}
	source, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer source.Close()
	sealed := filepath.Join(control, "image.tar.gz")
	file, err := os.OpenFile(sealed, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(source, pin.ArchiveBytes+1))
	err = errors.Join(copyErr, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	if n != pin.ArchiveBytes || hex.EncodeToString(hasher.Sum(nil)) != pin.ArchiveSHA256 {
		return errors.New("The worker image archive failed the fixed release checksum; no image was loaded")
	}
	if output, err := run("image", "load", "--input", sealed); err != nil {
		return fmt.Errorf("Load the fixed checkpoint image: %w %s", err, output)
	}
	return inspect()
}
