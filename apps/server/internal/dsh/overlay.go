package dsh

import "go.yaml.in/yaml/v3"

// Overlay uses the public sdk-minimal plugin configuration. Session logs and
// image attachments share one private generation mount; launcher home stays
// ephemeral so persisted data can never become next launch's configuration.
func Overlay(persona string, readOnly bool) ([]byte, error) {
	mode := "workspace-write"
	if readOnly {
		mode = "read-only"
	}
	rows := []map[string]any{
		{"id": "sandbox-policy", "config": map[string]any{"mode": mode, "workspaceRoot": "/workspace"}},
		{"id": "system-prompt", "config": map[string]any{"personaPrefix": persona}},
		{"id": "sessions", "config": map[string]any{"root": "/state/sessions", "compression": "none"}},
		{"id": "attachment-local", "config": map[string]any{"dshHome": "/state"}},
		{"id": "session-log-deepseek", "disabled": true},
		{"id": "llm-retry", "disabled": true},
		{"id": "terminal-bash", "config": map[string]any{"timeoutMs": 10000, "disposeGraceMs": 1000}},
		{"id": "persistent-bash", "config": map[string]any{"timeoutMs": 10000, "maxOutputChars": 16000}},
	}
	return yaml.Marshal(rows)
}
