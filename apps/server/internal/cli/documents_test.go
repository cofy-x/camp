package cli

import (
	"path/filepath"
	"testing"
)

func TestDocumentRuntimeUsesOnlyProcessConfiguration(t *testing.T) {
	cwd := t.TempDir()
	for _, value := range []string{"", "local-parser", filepath.Join(cwd, "external-parser")} {
		got := documentRuntime(func(key string) string {
			if key != "CAMP_DOCUMENT_RUNTIME" {
				t.Fatalf("unexpected configuration lookup %q", key)
			}
			return value
		}, cwd)
		want := value
		if value == "" {
			want = filepath.Join(cwd, "tools", "document-runtime")
		} else if !filepath.IsAbs(value) {
			want = filepath.Join(cwd, value)
		}
		if got != want {
			t.Fatalf("runtime %q: got %q, want %q", value, got, want)
		}
	}
}
