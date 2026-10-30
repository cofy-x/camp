package dsh

import (
	"go.yaml.in/yaml/v3"
	"path/filepath"
)

type Paths struct{ Workspace, State, Runtime string }

// RemoteOverlay retains the public SDK lifecycle while replacing every model
// file and process capability with Camp's per-Run authenticated tool providers.
func RemoteOverlay(persona string, readOnly bool, collaboration ...bool) ([]byte, error) {
	return RemoteOverlayAt(persona, readOnly, Paths{}, collaboration...)
}

func RemoteOverlayAt(persona string, readOnly bool, paths Paths, collaboration ...bool) ([]byte, error) {
	if paths.Workspace == "" {
		paths.Workspace = "/workspace"
	}
	if paths.State == "" {
		paths.State = "/state"
	}
	if paths.Runtime == "" {
		paths.Runtime = "/runtime"
	}
	module := func(name string) string { return filepath.Join(paths.Runtime, "camp-tools", name+".mjs") }
	mode := "workspace-write"
	if readOnly {
		mode = "read-only"
	}
	rows := []map[string]any{
		{"id": "sandbox-policy", "config": map[string]any{"mode": mode, "workspaceRoot": paths.Workspace}},
		{"id": "system-prompt", "config": map[string]any{"personaPrefix": persona}},
		{"id": "sessions", "config": map[string]any{"root": filepath.Join(paths.State, "sessions"), "compression": "none"}},
		{"id": "attachment-local", "config": map[string]any{"dshHome": paths.State}},
		{"id": "session-log-deepseek", "disabled": true},
		{"id": "llm-retry", "disabled": true},
	}
	for _, id := range []string{"filesystem", "subprocess", "pty", "terminal-bash", "terminal-pwsh", "persistent-bash", "persistent-pwsh", "jobs", "tool-working-directory"} {
		rows = append(rows, map[string]any{"id": id, "disabled": true})
	}
	rows = append(rows, map[string]any{"id": "tool-fs", "config": map[string]any{"read": true, "readImage": false, "write": !readOnly, "edit": !readOnly}})
	providers := []map[string]any{
		{"id": "camp-filesystem", "name": module("filesystem")},
		{"id": "camp-file-discovery", "name": module("discovery")},
	}
	if len(collaboration) > 0 && collaboration[0] {
		providers = append(providers, map[string]any{"id": "camp-coordination", "name": module("coordination")})
	}
	if !readOnly {
		providers = append(providers,
			map[string]any{"id": "fs-observation-policy", "name": "@deepseek-ai/dsh-fs-observation-policy"},
			map[string]any{"id": "camp-shell", "name": module("shell")},
			map[string]any{"id": "shell-env", "name": "@deepseek-ai/dsh-shell-env"},
			map[string]any{"id": "camp-bash", "name": "@deepseek-ai/dsh-tool-bash", "config": map[string]any{"enableRunInBackground": false, "promoteOnTimeout": false}},
		)
	}
	rows = append(rows, map[string]any{"insert": providers})
	return yaml.Marshal(rows)
}
