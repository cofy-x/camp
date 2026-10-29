// Package layerfs integrates the pinned LayerFS runtime with Camp-owned execution.
package layerfs

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const dockerAPIVersion = "/v1.54"

var containerIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var containerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

// Docker talks only to the operator-selected local socket. Container names are
// resolved once; all subsequent operations require the full frozen identity.
type Docker struct {
	client *http.Client
	socket string
}

func NewDocker(socket string) (*Docker, error) {
	if !strings.HasPrefix(socket, "/") || strings.ContainsRune(socket, '\x00') {
		return nil, errors.New("Docker requires an absolute local socket path")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}, MaxIdleConns: 8, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 15 * time.Second}
	return &Docker{client: &http.Client{Transport: transport}, socket: socket}, nil
}

func (d *Docker) Close() { d.client.CloseIdleConnections() }

// Limit bounds the whole Run, including daemon and detached descendants. These
// resource limits are applied and inspected before any model tool is admitted.
func (d *Docker) Limit(ctx context.Context, id string) error {
	if err := validContainer(id); err != nil {
		return err
	}
	const memory = 1 << 30
	const cpus = 2_000_000_000
	const pids = 256
	if err := d.json(ctx, http.MethodPost, "/containers/"+id+"/update", map[string]any{"Memory": memory, "MemorySwap": memory, "NanoCpus": cpus, "PidsLimit": pids}, nil); err != nil {
		return err
	}
	var inspect struct {
		HostConfig struct {
			Memory     int64 `json:"Memory"`
			MemorySwap int64 `json:"MemorySwap"`
			NanoCpus   int64 `json:"NanoCpus"`
			PidsLimit  int64 `json:"PidsLimit"`
		} `json:"HostConfig"`
	}
	if err := d.json(ctx, http.MethodGet, "/containers/"+id+"/json", nil, &inspect); err != nil {
		return err
	}
	if inspect.HostConfig.Memory != memory || inspect.HostConfig.MemorySwap != memory || inspect.HostConfig.NanoCpus != cpus || inspect.HostConfig.PidsLimit != pids {
		return errors.New("Docker did not confirm the Run's process, memory, and CPU limits")
	}
	return nil
}

func (d *Docker) request(ctx context.Context, method, endpoint string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+dockerAPIVersion+endpoint, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Docker %s: %w", method, err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		defer res.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("Docker %s returned %d: %s", method, res.StatusCode, strings.TrimSpace(string(data)))
	}
	return res, nil
}

func (d *Docker) json(ctx context.Context, method, endpoint string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	res, err := d.request(ctx, method, endpoint, body, "application/json")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if output == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		return err
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(output)
}

type containerState struct {
	ID    string `json:"Id"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
}

func (d *Docker) Resolve(ctx context.Context, name string) (string, error) {
	if !containerNamePattern.MatchString(name) {
		return "", errors.New("Invalid LayerFS container name")
	}
	var state containerState
	if err := d.json(ctx, http.MethodGet, "/containers/"+name+"/json", nil, &state); err != nil {
		return "", err
	}
	if !containerIDPattern.MatchString(state.ID) || !state.State.Running {
		return "", errors.New("LayerFS container is not running with a valid identity")
	}
	return state.ID, nil
}

func validContainer(id string) error {
	if !containerIDPattern.MatchString(id) {
		return errors.New("Docker operations require a frozen full container ID")
	}
	return nil
}

// InstallFile copies a single trusted file, without accepting archive metadata
// from users or agents. Run helpers and references are installed before admission.
func (d *Docker) InstallFile(ctx context.Context, id, destination string, data []byte, mode int64) error {
	if err := validContainer(id); err != nil {
		return err
	}
	if !strings.HasPrefix(destination, "/") || path.Clean(destination) != destination || destination == "/" || strings.ContainsRune(destination, '\x00') {
		return errors.New("Invalid container installation path")
	}
	var archive bytes.Buffer
	w := tar.NewWriter(&archive)
	if err := w.WriteHeader(&tar.Header{Name: path.Base(destination), Mode: mode & 0777, Size: int64(len(data)), Typeflag: tar.TypeReg, Uid: 0, Gid: 0}); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	res, err := d.request(ctx, http.MethodPut, "/containers/"+id+"/archive?path="+url.QueryEscape(path.Dir(destination))+"&noOverwriteDirNonDir=1", &archive, "application/x-tar")
	if err != nil {
		return err
	}
	return res.Body.Close()
}

// Exec runs a foreground helper. Docker stream framing is independent of the
// helper's own authenticated completion protocol; a Docker exit alone is not
// confirmation that the helper reaped command descendants.
func (d *Docker) Exec(ctx context.Context, id, user string, argv []string, stdout, stderr io.Writer) (int, error) {
	return d.ExecInput(ctx, id, user, argv, nil, stdout, stderr)
}

// ExecInput uses Docker's documented hijacked stream to close stdin without
// closing stdout. Requests are never interpolated into shell arguments.
func (d *Docker) ExecInput(ctx context.Context, id, user string, argv []string, input io.Reader, stdout, stderr io.Writer) (int, error) {
	if err := validContainer(id); err != nil {
		return -1, err
	}
	if len(argv) == 0 || user == "" {
		return -1, errors.New("Docker execution requires explicit arguments and user")
	}
	var created struct {
		ID string `json:"Id"`
	}
	err := d.json(ctx, http.MethodPost, "/containers/"+id+"/exec", map[string]any{
		"AttachStdout": true, "AttachStderr": true, "AttachStdin": input != nil,
		"Tty": false, "User": user, "Cmd": argv, "WorkingDir": "/",
	}, &created)
	if err != nil {
		return -1, err
	}
	if !containerIDPattern.MatchString(created.ID) {
		return -1, errors.New("Docker returned an invalid exec identity")
	}
	if input != nil {
		err = d.attach(ctx, created.ID, input, stdout, stderr)
	} else {
		var res *http.Response
		res, err = d.request(ctx, http.MethodPost, "/exec/"+created.ID+"/start", strings.NewReader(`{"Detach":false,"Tty":false}`), "application/json")
		if err == nil {
			err = errors.Join(readDockerFrames(res.Body, stdout, stderr), res.Body.Close())
		}
	}
	if err != nil {
		return -1, err
	}
	var status struct {
		Running  bool `json:"Running"`
		ExitCode int  `json:"ExitCode"`
	}
	if err := d.json(ctx, http.MethodGet, "/exec/"+created.ID+"/json", nil, &status); err != nil {
		return -1, err
	}
	if status.Running {
		return -1, errors.New("Docker closed output before execution ended")
	}
	return status.ExitCode, nil
}

func (d *Docker) attach(ctx context.Context, execID string, input io.Reader, stdout, stderr io.Writer) error {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", d.socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	req, err := http.NewRequest(http.MethodPost, "http://docker"+dockerAPIVersion+"/exec/"+execID+"/start", strings.NewReader(`{"Detach":false,"Tty":false}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	if err := req.Write(conn); err != nil {
		return err
	}
	reader := bufio.NewReader(conn)
	res, err := http.ReadResponse(reader, req)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusSwitchingProtocols {
		_ = res.Body.Close()
		return fmt.Errorf("Docker stdin stream did not upgrade: %d", res.StatusCode)
	}
	written := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(conn, input)
		closeErr := conn.(*net.UnixConn).CloseWrite()
		written <- errors.Join(copyErr, closeErr)
	}()
	readErr := readDockerFrames(reader, stdout, stderr)
	_ = conn.Close()
	return errors.Join(readErr, <-written)
}

func readDockerFrames(r io.Reader, stdout, stderr io.Writer) error {
	var header [8]byte
	for {
		_, err := io.ReadFull(r, header[:])
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("Incomplete Docker output frame: %w", err)
		}
		if header[1] != 0 || header[2] != 0 || header[3] != 0 || (header[0] != 1 && header[0] != 2) {
			return errors.New("Invalid Docker output frame")
		}
		size := binary.BigEndian.Uint32(header[4:])
		if size > 16<<20 {
			return errors.New("Docker output frame exceeds 16 MiB")
		}
		out := stdout
		if header[0] == 2 {
			out = stderr
		}
		if out == nil {
			out = io.Discard
		}
		if _, err := io.CopyN(out, r, int64(size)); err != nil {
			return fmt.Errorf("Docker output could not be drained: %w", err)
		}
	}
}

// Stop waits for the entire container to stop. A failed confirmation is a
// cleanup failure and must never be reported as a successful cancellation.
func (d *Docker) Stop(ctx context.Context, id string, grace time.Duration) error {
	if err := validContainer(id); err != nil {
		return err
	}
	seconds := max(1, int(grace/time.Second))
	if err := d.json(ctx, http.MethodPost, "/containers/"+id+"/stop?t="+strconv.Itoa(seconds), nil, nil); err != nil {
		return err
	}
	var state containerState
	if err := d.json(ctx, http.MethodGet, "/containers/"+id+"/json", nil, &state); err != nil {
		return err
	}
	if state.ID != id || state.State.Running {
		return errors.New("Container stop could not be confirmed")
	}
	return nil
}
