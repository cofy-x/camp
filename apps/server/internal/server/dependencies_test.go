package server

import (
	"os/exec"
	"strings"
	"testing"
)

func TestProductionServerDoesNotDependOnLocalExecutionHarness(t *testing.T) {
	output, err := exec.Command("go", "list", "-deps", "github.com/cofy-x/camp/apps/server/internal/server").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range strings.Fields(string(output)) {
		if strings.HasSuffix(name, "/internal/runs") {
			t.Fatal("Production Server depends on the local regression lifecycle", name)
		}
	}
}
