package dsh

import "go.yaml.in/yaml/v3"

// RemoteOverlay retains the public SDK lifecycle while replacing every model
// file and process capability with Camp's per-Run authenticated tool providers.
func RemoteOverlay(persona string, readOnly bool, collaboration ...bool) ([]byte, error) {
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
	}
	for _, id := range []string{"filesystem", "subprocess", "pty", "terminal-bash", "terminal-pwsh", "persistent-bash", "persistent-pwsh", "jobs", "tool-working-directory"} {
		rows = append(rows, map[string]any{"id": id, "disabled": true})
	}
	rows = append(rows, map[string]any{"id": "tool-fs", "config": map[string]any{"read": true, "readImage": false, "write": !readOnly, "edit": !readOnly}})
	providers := []map[string]any{
		{"id": "camp-filesystem", "name": "/runtime/camp-tools/filesystem.mjs"},
		{"id": "camp-file-discovery", "name": "/runtime/camp-tools/discovery.mjs"},
	}
	if len(collaboration) > 0 && collaboration[0] {
		providers = append(providers, map[string]any{"id": "camp-coordination", "name": "/runtime/camp-tools/coordination.mjs"})
	}
	if !readOnly {
		providers = append(providers,
			map[string]any{"id": "fs-observation-policy", "name": "@deepseek-ai/dsh-fs-observation-policy"},
			map[string]any{"id": "camp-shell", "name": "/runtime/camp-tools/shell.mjs"},
			map[string]any{"id": "shell-env", "name": "@deepseek-ai/dsh-shell-env"},
			map[string]any{"id": "camp-bash", "name": "@deepseek-ai/dsh-tool-bash", "config": map[string]any{"enableRunInBackground": false, "promoteOnTimeout": false}},
		)
	}
	rows = append(rows, map[string]any{"insert": providers})
	return yaml.Marshal(rows)
}
