package server

import (
	"encoding/json"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/documenttext"
)

func TestDocumentReadinessUsesExistingSettingsVocabulary(t *testing.T) {
	for _, test := range []struct{ reader, setting string }{{"unavailable", "missing"}, {"ready", "ready"}, {"unknown", "unknown"}, {"unexpected-reader-state", "error"}} {
		component := documentDiagnostic(documenttext.Check{Format: "pdf", Status: test.reader, Detail: "Reader observation", CheckedAt: "observed-time"})
		data, err := json.Marshal(component)
		if err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		if response["status"] != test.setting || response["detail"] != "Reader observation" || response["checkedAt"] != "observed-time" {
			t.Fatalf("reader state escaped its public diagnostic adapter: %s", data)
		}
	}
}
