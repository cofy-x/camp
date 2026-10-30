//go:build darwin

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestMacSourceCaptureAcceptsSystemAliasButRejectsUserAlias(t *testing.T) {
	source, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(source, "/private/var/") {
		t.Skip("requires the Mac system temporary-directory alias")
	}
	alias := strings.TrimPrefix(source, "/private")
	if err := os.WriteFile(filepath.Join(source, "native.bin"), []byte{0, 255, 128}, 0600); err != nil {
		t.Fatal(err)
	}
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{config: Config{DataDir: directory, Sources: map[string]string{"native": alias}}}
	items := d.sources()
	if len(items) != 1 || !items[0].Available || items[0].Revision != sourceRevision(alias) {
		t.Fatal("system alias lost source availability or its frozen identity", items)
	}
	operation := filepath.Join(directory, "capture")
	if err := os.Mkdir(operation, 0700); err != nil {
		t.Fatal(err)
	}
	_, archive, err := d.capture(context.Background(), p.Command{Kind: "source.capture", Source: &p.SourceCapture{SourceID: "native", Revision: sourceRevision(alias)}}, operation, func(string, string) error { return nil })
	if err != nil || archive == "" {
		t.Fatal("native source capture failed", err)
	}
	if data, err := os.ReadFile(filepath.Join(source, "native.bin")); err != nil || string(data) != string([]byte{0, 255, 128}) {
		t.Fatal("source capture changed original bytes", err)
	}
	linked := filepath.Join(directory, "user-alias")
	if err := os.Symlink(source, linked); err != nil {
		t.Fatal(err)
	}
	d.config.Sources["native"] = linked
	if d.sources()[0].Available {
		t.Fatal("a user-created symlink expanded source authority")
	}
}
