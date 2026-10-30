package nativeexecutor

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	"github.com/cofy-x/camp/apps/server/internal/runnertools"
	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
)

func (e *Executor) verifyChecks(ctx context.Context, sandbox runnertools.Backend, profile projectenv.Profile, result *Result, record Emit) error {
	if len(profile.Checks) > 0 {
		if err := record("status", "checking"); err != nil {
			return err
		}
	}
	code := 0
	var resultErr error
	for _, check := range profile.Checks {
		if err := ctx.Err(); err != nil {
			resultErr = err
			code = 1
			break
		}
		args := []string{"node", "--test"}
		if profile.Kind == "command" {
			args = append([]string(nil), check.Arguments...)
		} else if check.Script != "" {
			args = []string{"pnpm", "run", check.Script}
		}
		if args[0] == "/node" {
			args[0] = "node"
		}
		if args[0] == "/pnpm" {
			args[0] = "pnpm"
		}
		item := projectenv.CheckResult{Name: check.Name, Command: projectenv.CheckCommand(check), Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		checkCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		frame, runErr := sandbox.Call(checkCtx, sandboxexec.Request{Operation: "command", Argv: args, Cwd: path.Join("/workspace", check.Directory), TimeoutMS: 120000, MaxOutputBytes: 8 << 20}, func(frame sandboxexec.Frame) error {
			if frame.Type != "stdout" && frame.Type != "stderr" {
				return nil
			}
			text := string(frame.Data)
			item.Output = bounded(item.Output+text, 16<<10)
			return record("check", "["+check.Name+"] "+text)
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
			item.Output = bounded(item.Output+"\n"+runErr.Error(), 16<<10)
		}
		item.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		result.Checks = append(result.Checks, item)
		if err := record("check", fmt.Sprintf("[Camp] %s: %s (%s).\n", item.Name, item.Status, item.Command)); err != nil {
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
	result.TestExitCode = &code
	return resultErr
}
