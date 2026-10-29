package cli

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestSourceFlagsMatchRunnerProtocolBounds(t *testing.T) {
	directory := t.TempDir()
	for _, alias := range []string{"", "Notes", "1notes", ".", "notes.name", strings.Repeat("a", p.MaxSourceIDLength+1)} {
		var flags sourceFlags
		if err := flags.Set(alias + "=" + directory); err == nil || len(flags) != 0 {
			t.Fatal("invalid alias entered private configuration", alias, err)
		}
	}
	var flags sourceFlags
	if err := flags.Set(strings.Repeat("a", p.MaxSourceIDLength) + "=" + directory); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < p.MaxSources; i++ {
		if err := flags.Set(fmt.Sprintf("source-%d=%s", i, directory)); err != nil {
			t.Fatal(err)
		}
	}
	if err := flags.Set("overflow=" + directory); err == nil || len(flags) != p.MaxSources {
		t.Fatal("too many source aliases accepted", len(flags), err)
	}
}
