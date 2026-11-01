package launcher

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestEntrypointHelpAndVersionNeedNoInstallation(t *testing.T) {
	if mode := os.Getenv("CAMP_TEST_ENTRYPOINT"); mode != "" {
		os.Args = []string{"camp", mode}
		Main("v0.1.0-alpha.7")
		return
	}
	for _, mode := range []string{"version", "--version", "help", "--help", "-h"} {
		t.Run(mode, func(t *testing.T) {
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cache := t.TempDir()
			command := exec.Command(binary, "-test.run=^TestEntrypointHelpAndVersionNeedNoInstallation$")
			command.Env = append(os.Environ(), "CAMP_TEST_ENTRYPOINT="+mode, "XDG_CACHE_HOME="+cache)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, output)
			}
			text := string(output)
			if strings.Contains(mode, "version") {
				if !strings.HasPrefix(text, "camp v0.1.0-alpha.7\n") {
					t.Fatal(text)
				}
			} else if !strings.Contains(text, "supported Mac or Linux Runner") {
				t.Fatal(text)
			}
			if strings.Contains(text, "Installing Camp") {
				t.Fatal("informational command attempted installation", text)
			}
			entries, err := os.ReadDir(cache)
			if err != nil || len(entries) != 0 {
				t.Fatal("informational command changed the cache", entries, err)
			}
		})
	}
}
