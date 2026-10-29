package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (m *Manager) environmentOptions(e *execution, dir string) projectenv.Options {
	m.mu.Lock()
	resourceID := e.run.ResourceID
	m.mu.Unlock()
	options := projectenv.Options{NodePath: m.config.NodePath, ProjectDir: filepath.Join(dir, "project"), CacheDir: filepath.Join(filepath.Dir(m.dir), "environments", resourceID), Lock: m.lock, Output: func(text string) error { return m.record(e, "environment", text) }}
	if e.environment != nil {
		options.Profile = &e.environment.Profile
		options.PnpmPath = e.environment.PnpmPath
	}
	return options
}

func (m *Manager) prepareEnvironment(ctx context.Context, e *execution, dir string) error {
	m.mu.Lock()
	encoded := append(json.RawMessage(nil), e.run.Environment...)
	m.mu.Unlock()
	if len(encoded) == 0 || string(encoded) == "null" || string(encoded) == "{}" {
		return m.record(e, "environment", "[Camp] Checks: Not configured. No project environment is required for file work.\n")
	}
	var profile projectenv.Profile
	if err := json.Unmarshal(encoded, &profile); err != nil {
		return err
	}
	if err := projectenv.Validate(profile); err != nil {
		return err
	}
	options := m.environmentOptions(e, dir)
	options.Profile = &profile
	var err error
	if profile.Kind == "node" && profile.PackageManager != "node" {
		options.PnpmPath, err = projectenv.FindPnpm(m.config.RuntimeDir)
		if err != nil {
			return err
		}
	}
	if err := m.record(e, "environment", "[Camp] Preparing the configured environment.\n"); err != nil {
		return err
	}
	e.environment, err = projectenv.Prepare(ctx, options)
	return err
}

func (m *Manager) verifyEnvironment(ctx context.Context, e *execution) error {
	checks := []projectenv.CheckResult{}
	code := 0
	var resultErr error
	for _, check := range e.environment.Profile.Checks {
		if err := ctx.Err(); err != nil {
			resultErr = err
			code = 1
			break
		}
		args := []string{"node", "--test"}
		if e.environment.Profile.Kind == "command" {
			args = append([]string(nil), check.Arguments...)
		} else if check.Script != "" {
			args = []string{"pnpm", "run", check.Script}
		}
		// Historical environment descriptions used Bubblewrap's fixed tool aliases.
		if args[0] == "/node" {
			args[0] = "node"
		}
		if args[0] == "/pnpm" {
			args[0] = "pnpm"
		}
		item := projectenv.CheckResult{Name: check.Name, Command: projectenv.CheckCommand(check), Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		checks = append(checks, item)
		save := func() error {
			checks[len(checks)-1] = item
			encoded, _ := json.Marshal(checks)
			return m.update(e, func(r *storage.Run) { r.Checks = encoded })
		}
		if err := save(); err != nil {
			return err
		}
		checkCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		frame, runErr := e.sandbox.Call(checkCtx, sandboxexec.Request{Operation: "command", Argv: args, Cwd: path.Join("/workspace", check.Directory), TimeoutMS: 120000, MaxOutputBytes: 8 << 20}, func(frame sandboxexec.Frame) error {
			if frame.Type != "stdout" && frame.Type != "stderr" {
				return nil
			}
			text := m.redact(string(frame.Data))
			item.Output = checkOutput(item.Output + text)
			return m.record(e, "check", "["+check.Name+"] "+text)
		})
		contextErr := checkCtx.Err()
		cancel()
		exitCode := 1
		if value, ok := frame.Code.(float64); ok {
			exitCode = int(value)
		}
		item.ExitCode = &exitCode
		item.Status = "passed"
		if runErr != nil || exitCode != 0 {
			item.Status = "failed"
			code = 1
		}
		if contextErr != nil || frame.Reason == "timeout" || frame.Reason == "cancelled" {
			item.Status = "interrupted"
			code = 1
		}
		if runErr != nil {
			item.Output = checkOutput(item.Output + "\n" + m.redact(runErr.Error()))
		}
		item.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := save(); err != nil {
			return err
		}
		if err := m.record(e, "check", fmt.Sprintf("[Camp] %s: %s (%s).\n", item.Name, item.Status, item.Command)); err != nil {
			return err
		}
		if runErr != nil || ctx.Err() != nil {
			resultErr = errors.Join(runErr, ctx.Err())
			break
		}
	}
	if code != 0 && resultErr == nil {
		resultErr = errors.New("One or more configured checks did not pass")
	}
	return errors.Join(resultErr, m.update(e, func(r *storage.Run) { r.TestExitCode = &code }))
}

func checkOutput(text string) string {
	text = strings.ToValidUTF8(text, "�")
	const limit = 16 << 10
	const marker = "[Earlier check output omitted]\n"
	if len(text) <= limit {
		return text
	}
	return marker + strings.ToValidUTF8(text[len(text)-limit+len(marker)+3:], "�")
}
