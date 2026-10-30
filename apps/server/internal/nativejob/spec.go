// Package nativejob runs one owned native process tree behind a private local
// transport. It grants no Runner enrollment, model or publication authority.
package nativejob

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/processowner"
	"github.com/cofy-x/camp/apps/server/internal/seatbelt"
)

type Spec struct {
	Argv                           []string              `json:"argv"`
	Env                            []string              `json:"env"`
	Directory                      string                `json:"directory"`
	Policy                         seatbelt.Policy       `json:"policy"`
	TimeoutMS                      int64                 `json:"timeoutMs"`
	MaxOutput                      int64                 `json:"maxOutput"`
	Helper                         string                `json:"helper"`
	Client                         processowner.Identity `json:"client"`
	Endpoint, Token, Label, Domain string
}

type Result struct {
	Code      int    `json:"code"`
	Clean     bool   `json:"clean"`
	Reason    string `json:"reason,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type frame struct {
	Type   string                `json:"type"`
	Token  string                `json:"token,omitempty"`
	Owner  processowner.Identity `json:"owner,omitempty"`
	Data   []byte                `json:"data,omitempty"`
	Result *Result               `json:"result,omitempty"`
}

func readSpec(name string) (Spec, error) {
	var spec Spec
	info, err := os.Lstat(name)
	if err != nil || !filepath.IsAbs(name) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 64<<10 {
		return spec, errors.New("Native job configuration must be a private bounded regular file")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return spec, err
	}
	if err = json.Unmarshal(data, &spec); err != nil || len(spec.Argv) == 0 || !filepath.IsAbs(spec.Argv[0]) || !filepath.IsAbs(spec.Directory) || spec.TimeoutMS <= 0 || spec.TimeoutMS > 900000 || spec.MaxOutput <= 0 || spec.MaxOutput > 16<<20 {
		return spec, errors.New("Invalid native job request")
	}
	return spec, nil
}

func save(name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}

// Command presents normal stdio to DSH; the client/helper owns its launchd job
// and confirms kernel cleanup before it exits. No secret is put in argv.
func Command(helper, root string, spec Spec) (*exec.Cmd, string, error) {
	if !filepath.IsAbs(helper) || !filepath.IsAbs(root) {
		return nil, "", errors.New("Native helper and job storage require absolute paths")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, "", err
	}
	directory, err := os.MkdirTemp(root, "job-")
	if err != nil {
		return nil, "", err
	}
	spec.Helper = helper
	if spec.TimeoutMS == 0 {
		spec.TimeoutMS = int64((180 * time.Second) / time.Millisecond)
	}
	if spec.MaxOutput == 0 {
		spec.MaxOutput = 8 << 20
	}
	name := filepath.Join(directory, "request.json")
	if err := save(name, spec); err != nil {
		return nil, "", err
	}
	cmd := exec.Command(helper, "native-client", name)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + directory, "TMPDIR=" + directory}
	cmd.WaitDelay = 2 * time.Second
	return cmd, directory, nil
}

func Run(ctx context.Context, helper, root string, spec Spec, input io.Reader, stdout, stderr io.Writer) (Result, error) {
	cmd, directory, err := Command(helper, root, spec)
	if err != nil {
		return Result{}, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, stdout, stderr
	if err = cmd.Start(); err != nil {
		return Result{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	case <-done:
	}
	var result Result
	data, readErr := os.ReadFile(filepath.Join(directory, "result.json"))
	if readErr != nil || json.Unmarshal(data, &result) != nil || !result.Clean {
		return result, errors.Join(errors.New("Native command cleanup could not be confirmed"), readErr)
	}
	return result, nil
}
