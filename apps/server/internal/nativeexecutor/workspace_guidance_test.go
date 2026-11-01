package nativeexecutor

import (
	"strconv"
	"strings"
	"testing"
)

func TestExecutionGuidanceUsesTheCurrentAttemptNamespace(t *testing.T) {
	for _, tt := range []struct{ workspace, references string }{
		{"/workspace", "/references"},
		{"/Users/operator/Camp/attempt one/workspace", "/Users/operator/Camp/attempt one/references"},
		{"/Users/operator/Camp/attempt-two/workspace", ""},
	} {
		text := executionGuidance(tt.workspace, tt.references)
		for _, value := range []string{strconv.Quote(tt.workspace), "defaults to '.'", "file and shell operations", "session continuation"} {
			if !strings.Contains(text, value) {
				t.Fatalf("missing %q: %s", value, text)
			}
		}
		if tt.references != "" && !strings.Contains(text, strconv.Quote(tt.references)) {
			t.Fatal("reference root missing", text)
		}
		if tt.references == "" && strings.Contains(text, "reference evidence is under") {
			t.Fatal("advertised a missing reference root", text)
		}
		if tt.workspace != "/workspace" && (strings.Contains(text, `is "/workspace"`) || strings.Contains(text, `under "/references"`)) {
			t.Fatal("logical alias advertised as a native directory", text)
		}
	}
}
