package runnerprotocol

import (
	"strings"
	"testing"
)

func TestInstallationProvenanceBounds(t *testing.T) {
	valid := Installation{Version: "v0.1.0-alpha.10", SourceCommit: strings.Repeat("a", 40), RuntimeSHA256: strings.Repeat("b", 64)}
	if ValidateInstallation(nil) != nil || ValidateInstallation(&valid) != nil {
		t.Fatal("Valid diagnostic provenance was refused")
	}
	for _, mutate := range []func(*Installation){
		func(v *Installation) { v.Version = "" },
		func(v *Installation) { v.Version = "v" + strings.Repeat("a", 81) },
		func(v *Installation) { v.Version = "v1\nprivate" },
		func(v *Installation) { v.Version = "v1/<private>" },
		func(v *Installation) { v.SourceCommit = strings.Repeat("A", 40) },
		func(v *Installation) { v.SourceCommit = "main" },
		func(v *Installation) { v.RuntimeSHA256 = strings.Repeat("a", 63) },
	} {
		invalid := valid
		mutate(&invalid)
		if ValidateInstallation(&invalid) == nil {
			t.Fatal("Invalid provenance was accepted", invalid)
		}
	}
}
