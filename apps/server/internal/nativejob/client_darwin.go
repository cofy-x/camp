//go:build darwin

package nativejob

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/processowner"
)

type wire struct {
	mu         sync.Mutex
	connection net.Conn
}

func (w *wire) send(f frame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	data, err := json.Marshal(f)
	if err != nil || len(data) > 128<<10 {
		return errors.New("Native job frame exceeded its bound")
	}
	_ = w.connection.SetWriteDeadline(time.Now().Add(2 * time.Second))
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	if _, err = w.connection.Write(length[:]); err != nil {
		return err
	}
	_, err = w.connection.Write(data)
	return err
}

func receive(connection net.Conn) (frame, error) {
	var f frame
	var length [4]byte
	if _, err := io.ReadFull(connection, length[:]); err != nil {
		return f, err
	}
	n := binary.BigEndian.Uint32(length[:])
	if n == 0 || n > 128<<10 {
		return f, errors.New("Native job frame exceeded its bound")
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(connection, data); err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, err
	}
	if len(f.Data) > 64<<10 {
		return f, errors.New("Native job payload exceeded its bound")
	}
	return f, nil
}

func Client(ctx context.Context, name string, input io.Reader, output, diagnostic io.Writer) (int, error) {
	spec, err := readSpec(name)
	if err != nil {
		return 1, err
	}
	self, err := processowner.Observe(os.Getpid())
	if err != nil {
		return 1, err
	}
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return 1, err
	}
	defer listener.Close()
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return 1, err
	}
	spec.Client, spec.Endpoint, spec.Token = self, listener.Addr().String(), hex.EncodeToString(secret[:])
	sum := sha256.Sum256([]byte(name))
	spec.Label = fmt.Sprintf("dev.cofy.camp.job.%x", sum[:12])
	spec.Domain = fmt.Sprintf("gui/%d", os.Getuid())
	if err := exec.Command("/bin/launchctl", "print", spec.Domain).Run(); err != nil {
		spec.Domain = fmt.Sprintf("user/%d", os.Getuid())
	}
	dir := filepath.Dir(name)
	if err = save(filepath.Join(dir, "client.json"), self); err != nil {
		return 1, err
	}
	launch := filepath.Join(dir, "launch.json")
	if err = save(launch, spec); err != nil {
		return 1, err
	}
	escape := func(s string) string { var b strings.Builder; _ = xml.EscapeText(&b, []byte(s)); return b.String() }
	plist := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>` + escape(spec.Label) + `</string><key>ProgramArguments</key><array><string>` + escape(spec.Helper) + `</string><string>native-job</string><string>` + escape(launch) + `</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><false/><key>StandardOutPath</key><string>` + escape(filepath.Join(dir, "worker.stdout")) + `</string><key>StandardErrorPath</key><string>` + escape(filepath.Join(dir, "worker.stderr")) + `</string></dict></plist>`
	plistPath := filepath.Join(dir, "job.plist")
	if err = os.WriteFile(plistPath, []byte(plist), 0600); err != nil {
		return 1, err
	}
	if out, err := exec.Command("/bin/launchctl", "bootstrap", spec.Domain, plistPath).CombinedOutput(); err != nil {
		return 1, fmt.Errorf("Native job startup failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	defer func() { _ = exec.Command("/bin/launchctl", "bootout", spec.Domain+"/"+spec.Label).Run() }()
	_ = listener.SetDeadline(time.Now().Add(15 * time.Second))
	var connection *net.TCPConn
	var owner processowner.Identity
	for {
		connection, err = listener.AcceptTCP()
		if err != nil {
			return 1, errors.New("Native job did not establish its private channel")
		}
		_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
		hello, readErr := receive(connection)
		err = readErr
		if err == nil && hello.Type == "hello" && subtle.ConstantTimeCompare([]byte(hello.Token), []byte(spec.Token)) == 1 {
			owner = hello.Owner
			break
		}
		_ = connection.Close()
	}
	defer connection.Close()
	observed, err := processowner.Observe(owner.PID)
	if err != nil || observed != owner || owner.Boot != self.Boot || owner.Coalition == self.Coalition {
		return 1, errors.New("Native job has no independently verified process ownership")
	}
	if err = save(filepath.Join(dir, "owner.json"), owner); err != nil {
		return 1, err
	}
	_ = connection.SetReadDeadline(time.Time{})
	w := &wire{connection: connection}
	if err = w.send(frame{Type: "admit"}); err != nil {
		return 1, err
	}
	go func() {
		buffer := make([]byte, 16<<10)
		for {
			n, readErr := input.Read(buffer)
			if n > 0 {
				if w.send(frame{Type: "stdin", Data: buffer[:n]}) != nil {
					return
				}
			}
			if readErr != nil {
				_ = w.send(frame{Type: "stdin-eof"})
				return
			}
		}
	}()
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = w.send(frame{Type: "cancel"})
		case <-watchDone:
		}
	}()
	var result Result
	for {
		f, readErr := receive(connection)
		err = readErr
		if err != nil {
			break
		}
		if len(f.Data) > 64<<10 {
			err = errors.New("Native job output frame exceeded its bound")
			break
		}
		switch f.Type {
		case "stdout":
			_, err = output.Write(f.Data)
		case "stderr":
			_, err = diagnostic.Write(f.Data)
		case "exit":
			if f.Result == nil {
				err = errors.New("Native job omitted its result")
			} else {
				result = *f.Result
			}
		default:
			err = errors.New("Native job returned an invalid frame")
		}
		if err != nil || f.Type == "exit" {
			break
		}
	}
	cleanupCtx, stop := context.WithTimeout(context.Background(), 8*time.Second)
	cleanupErr := (processowner.Owner{Root: owner}).Terminate(cleanupCtx)
	stop()
	if err != nil || cleanupErr != nil || !result.Clean {
		return 1, errors.Join(errors.New("Native process cleanup is unconfirmed"), err, cleanupErr)
	}
	if err = save(filepath.Join(dir, "result.json"), result); err != nil {
		return 1, err
	}
	return result.Code, nil
}

// Reconcile only visits this Run's private job receipts. An uncertain result is
// not made successful merely because its processes can now be terminated.
func Reconcile(ctx context.Context, root string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "job-") {
			return errors.New("Unrecognized native job storage")
		}
		dir := filepath.Join(root, entry.Name())
		data, err := os.ReadFile(filepath.Join(dir, "owner.json"))
		if err != nil {
			return errors.New("Native job ownership was not confirmed")
		}
		var owner processowner.Identity
		if json.Unmarshal(data, &owner) != nil {
			return errors.New("Invalid native process receipt")
		}
		if err := (processowner.Owner{Root: owner}).Terminate(ctx); err != nil {
			return err
		}
		data, err = os.ReadFile(filepath.Join(dir, "result.json"))
		if err != nil {
			return errors.New("Native job result was not confirmed")
		}
		var result Result
		if json.Unmarshal(data, &result) != nil || !result.Clean {
			return errors.New("Native job cleanup was not confirmed")
		}
	}
	return nil
}

// Recover can establish process quiescence after a Runner restart. It never
// creates a success receipt or captures the old workspace. The daemon retains
// the interrupted operation and chooses whether its confirmed archive uploads.
func Recover(ctx context.Context, root string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	self, err := processowner.Observe(os.Getpid())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "job-") {
			return errors.New("Unrecognized native job storage")
		}
		dir := filepath.Join(root, entry.Name())
		readIdentity := func(name string) (processowner.Identity, bool, error) {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if errors.Is(err, os.ErrNotExist) {
				return processowner.Identity{}, false, nil
			}
			var identity processowner.Identity
			if err != nil || json.Unmarshal(data, &identity) != nil || identity.PID <= 1 || identity.Version == 0 || identity.Coalition == 0 || identity.Boot == "" {
				return identity, false, errors.New("Invalid native job identity receipt")
			}
			return identity, true, nil
		}
		client, found, err := readIdentity("client.json")
		if err != nil {
			return err
		}
		if found && client.Boot == self.Boot {
			if client.PID == self.PID {
				return errors.New("Native recovery cannot stop its own client")
			}
			if err := processowner.Signal(client, int(syscall.SIGKILL)); err != nil && !errors.Is(err, syscall.ESRCH) {
				return err
			}
		}
		for _, name := range []string{"owner.json", "worker-owner.json"} {
			owner, found, err := readIdentity(name)
			if err != nil {
				return err
			}
			if found && owner.Boot == self.Boot {
				if owner.Coalition == self.Coalition {
					return errors.New("Native recovery requires independently owned jobs")
				}
				if err := (processowner.Owner{Root: owner}).Terminate(ctx); err != nil {
					return err
				}
			}
		}
		launch, err := readSpec(filepath.Join(dir, "launch.json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		// readSpec deliberately hides filesystem errors; distinguish an absent
		// pre-admission file without treating malformed launch data as absent.
		if _, statErr := os.Lstat(filepath.Join(dir, "launch.json")); errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(filepath.Join(dir, "request.json")))
		expected := fmt.Sprintf("dev.cofy.camp.job.%x", sum[:12])
		if launch.Label != expected || launch.Domain != fmt.Sprintf("gui/%d", os.Getuid()) && launch.Domain != fmt.Sprintf("user/%d", os.Getuid()) {
			return errors.New("Invalid native job launch receipt")
		}
		_ = exec.CommandContext(ctx, "/bin/launchctl", "bootout", launch.Domain+"/"+launch.Label).Run()
		// A just-starting worker publishes its identity before admission. Check
		// again after unregistering the job to close that startup window.
		owner, found, err := readIdentity("worker-owner.json")
		if err != nil {
			return err
		}
		if found && owner.Boot == self.Boot {
			if err := (processowner.Owner{Root: owner}).Terminate(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}
