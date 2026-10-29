package projectenv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/resources"
)

type CheckResult struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	Status     string `json:"status"`
	ExitCode   *int   `json:"exitCode"`
	Output     string `json:"output"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}

type Verification struct {
	Checks      []CheckResult
	Directory   string
	Environment *Environment
}

// Verify runs all configured checks in one disposable, writable copy. The caller
// owns Directory and must remove it after retaining the check results.
// Normal check failures are recorded and do not suppress later checks.
func Verify(ctx context.Context, options Options, environment *Environment, update func(CheckResult) error) (Verification, error) {
	result := Verification{Checks: []CheckResult{}}
	if environment == nil {
		return result, errors.New("Prepare the project environment before verification")
	}
	directory, err := os.MkdirTemp(filepath.Dir(options.ProjectDir), ".verification-")
	if err != nil {
		return result, err
	}
	result.Directory = directory
	project := filepath.Join(directory, "project")
	if err := resources.CopySourceWithPolicy(ctx, options.ProjectDir, project, SnapshotPolicy(environment.Profile)); err != nil {
		return result, err
	}
	options.ProjectDir = project
	options.Profile = &environment.Profile
	prepared, err := Prepare(ctx, options)
	if err != nil {
		return result, err
	}
	result.Environment = prepared
	for _, check := range prepared.Profile.Checks {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		args := []string{"/node", "--test"}
		command := "node --test"
		if prepared.Profile.Kind == "command" {
			args = append([]string(nil), check.Arguments...)
			command = strings.Join(args, " ")
			if args[0] == "node" {
				args[0] = "/node"
			}
		} else if check.Script != "" {
			args = []string{"/pnpm", "run", check.Script}
			command = "pnpm run " + check.Script
		}
		if check.Directory != "" {
			command = "(" + check.Directory + ") " + command
		}
		item := CheckResult{Name: check.Name, Command: command, Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if update != nil {
			if err := update(item); err != nil {
				return result, err
			}
		}
		cmd, err := execution.ProjectCommand(execution.ProjectConfig{NodePath: options.NodePath, PnpmPath: prepared.PnpmPath, ProjectDir: project, Directory: check.Directory, Arguments: args, Dependencies: prepared.Mounts}, options.Lock)
		if err != nil {
			return result, err
		}
		checkContext, cancel := context.WithTimeout(ctx, 120*time.Second)
		var outputErr error
		code, runErr := execution.RunTests(checkContext, cmd, func(text string) error {
			item.Output = boundedOutput(item.Output + text)
			outputErr = emit(options, "["+check.Name+"] "+text)
			return outputErr
		})
		cancel()
		item.ExitCode = &code
		item.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		item.Status = "passed"
		if code != 0 || runErr != nil {
			item.Status = "failed"
		}
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			item.Status = "interrupted"
		}
		if runErr != nil {
			item.Output = boundedOutput(item.Output + "\n" + runErr.Error() + "\n")
		}
		result.Checks = append(result.Checks, item)
		if update != nil {
			if err := update(item); err != nil {
				return result, err
			}
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if outputErr != nil {
			return result, outputErr
		}

	}
	return result, nil
}

func boundedOutput(text string) string {
	text = strings.ToValidUTF8(text, "�")
	// Eight checks remain below the 1 MiB persisted JSON limit even when every
	// byte needs JSON escaping. The event stream retains the complete live output
	// subject to the run's existing event and aggregate limits.
	const limit = 16 << 10
	const marker = "[Earlier check output omitted]\n"
	if len(text) <= limit {
		return text
	}
	start := len(text) - limit + len(marker)
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return marker + text[start:]
}

// CheckCommand gives interfaces a truthful command before execution starts.
func CheckCommand(check Check) string {
	if len(check.Arguments) > 0 {
		command := strings.Join(check.Arguments, " ")
		if check.Directory != "" {
			command = "(" + check.Directory + ") " + command
		}
		return command
	}
	if check.Script == "" {
		if check.Directory != "" {
			return fmt.Sprintf("(%s) node --test", check.Directory)
		}
		return "node --test"
	}
	if check.Directory != "" {
		return fmt.Sprintf("(%s) pnpm run %s", check.Directory, check.Script)
	}
	return "pnpm run " + check.Script
}
