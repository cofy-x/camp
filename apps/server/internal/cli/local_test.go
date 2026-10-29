package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/server"
)

func TestBrowserAddressMatchesListenerAndPublicOrigin(t *testing.T) {
	for _, tt := range []struct{ address, certificate, origin, want string }{
		{"127.0.0.1:3001", "", "", "http://127.0.0.1:3001"},
		{"0.0.0.0:3443", "cert", "", "https://127.0.0.1:3443"},
		{"[::]:3001", "", "", "http://[::1]:3001"},
		{"127.0.0.1:3443", "cert", "https://camp.example.com", "https://camp.example.com"},
	} {
		got, err := browserURL(server.Config{Addr: tt.address, TLSCert: tt.certificate, PublicOrigin: tt.origin})
		if err != nil || got != tt.want {
			t.Fatalf("address = %q, %v; want %q", got, err, tt.want)
		}
	}
	for _, value := range []string{"javascript:alert(1)", "https://secret@camp.example", "https://camp.example/?key=secret", "https://camp.example/path"} {
		if _, err := browserURL(server.Config{PublicOrigin: value}); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe origin accepted or exposed", err)
		}
	}
}

func TestLocalCommandsDoNotInitializeStorageOrReadModelKeys(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new-team")
	t.Setenv("DATA_DIR", dir)
	t.Setenv("ADDR", "127.0.0.1:3001")
	t.Setenv("CAMP_ENV_FILE", filepath.Join(t.TempDir(), "must-not-be-read"))
	t.Setenv("CAMP_PUBLIC_ORIGIN", "")
	for _, args := range [][]string{{"doctor"}, {"doctor", "--help"}, {"open", "--print"}, {"open", "--help"}} {
		var output bytes.Buffer
		if err := Run(t.Context(), args, &output, "dev"); err != nil {
			t.Fatal(args, err)
		}
		if output.Len() == 0 {
			t.Fatal("missing actionable output")
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("informational command changed storage", err)
		}
	}
	if err := os.WriteFile(dir, []byte("preserve this file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Run(t.Context(), []string{"doctor"}, &bytes.Buffer{}, "dev"); err == nil {
		t.Fatal("doctor accepted a data file as a directory")
	}
}

func TestBrowserOpeningUsesAnArgumentWithoutAShell(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		var executable string
		var arguments []string
		address := "https://camp.example.com:3443"
		if err := openBrowser(t.Context(), platform, address, func(_ context.Context, name string, args ...string) error {
			executable, arguments = name, args
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if executable == "sh" || len(arguments) != 1 || arguments[0] != address || (platform == "darwin" && executable != "open") {
			t.Fatal("unexpected browser invocation", executable, arguments)
		}
	}
}
