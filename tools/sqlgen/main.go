// Command sqlgen generates and checks the complete, committed sqlc output.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	sqlcModule = "github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1"
	schemaDir  = "apps/server/internal/storage/migrations"
	queriesDir = "apps/server/internal/storage/queries"
	outputDir  = "apps/server/internal/storage/internal/sqlgen"
)

// Camp has one fixed output. Reject other configuration before running
// sqlc so an absolute or parent-relative output cannot escape the temporary tree.
type config struct {
	Version string `yaml:"version"`
	SQL     []struct {
		Engine  string `yaml:"engine"`
		Schema  string `yaml:"schema"`
		Queries string `yaml:"queries"`
		Gen     struct {
			Go struct {
				Package         string `yaml:"package"`
				Out             string `yaml:"out"`
				SQLPackage      string `yaml:"sql_package"`
				EmitEmptySlices bool   `yaml:"emit_empty_slices"`
			} `yaml:"go"`
		} `yaml:"gen"`
	} `yaml:"sql"`
}

func validateConfig(data []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var cfg config
	if err := decoder.Decode(&cfg); err != nil {
		return fmt.Errorf("read sqlc.yaml: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("sqlc.yaml must contain exactly one YAML document")
	}
	if cfg.Version != "2" || len(cfg.SQL) != 1 {
		return errors.New("sqlc.yaml must use version 2 with one SQLite Go output")
	}
	sql := cfg.SQL[0]
	goOutput := sql.Gen.Go
	if sql.Engine != "sqlite" || sql.Schema != schemaDir || sql.Queries != queriesDir ||
		goOutput.Out != outputDir || goOutput.Package != "sqlgen" || goOutput.SQLPackage != "database/sql" || !goOutput.EmitEmptySlices {
		return errors.New("sqlc.yaml no longer matches Camp's SQLite paths and Go output settings; update tools/sqlgen deliberately with the configuration")
	}
	return nil
}

// readFiles rejects symlinks so inputs and the owned output stay inside their
// declared trees. Missing output is treated as an empty set by the caller.
func readFiles(dir string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("expected a regular file: %s", path)
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files[relative], err = os.ReadFile(path)
		return err
	})
	return files, err
}

func writeFiles(dir string, files map[string][]byte) error {
	for name, data := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

type generator func(context.Context, string) error

func generate(ctx context.Context, root string, run generator) (map[string][]byte, error) {
	data, err := os.ReadFile(filepath.Join(root, "sqlc.yaml"))
	if err != nil {
		return nil, err
	}
	if err := validateConfig(data); err != nil {
		return nil, err
	}
	workspace, err := os.MkdirTemp("", "sqlgen-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workspace)
	if err := os.WriteFile(filepath.Join(workspace, "sqlc.yaml"), data, 0o644); err != nil {
		return nil, err
	}
	for _, source := range []string{schemaDir, queriesDir} {
		files, err := readFiles(filepath.Join(root, source))
		if err != nil {
			return nil, err
		}
		if err := writeFiles(filepath.Join(workspace, source), files); err != nil {
			return nil, err
		}
	}
	if err := run(ctx, workspace); err != nil {
		return nil, fmt.Errorf("generate with sqlc: %w", err)
	}
	files, err := readFiles(filepath.Join(workspace, outputDir))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("sqlc generated no files")
	}
	return files, nil
}

func differences(want, actual map[string][]byte) []string {
	var result []string
	for name, data := range want {
		current, exists := actual[name]
		if !exists {
			result = append(result, "missing "+name)
		} else if !bytes.Equal(data, current) {
			result = append(result, "changed "+name)
		}
	}
	for name := range actual {
		if _, exists := want[name]; !exists {
			result = append(result, "unexpected "+name)
		}
	}
	sort.Strings(result)
	return result
}

// Replace the owned output directory only after generation and staging succeed.
// Keep the old directory available for rollback if installing the stage fails.
func replaceOutput(dir string, files map[string][]byte) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".sqlgen-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := writeFiles(stage, files); err != nil {
		return err
	}
	backup := stage + "-previous"
	if err := os.Rename(dir, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(stage, dir); err != nil {
		if restoreErr := os.Rename(backup, dir); restoreErr != nil && !errors.Is(restoreErr, os.ErrNotExist) {
			return errors.Join(err, fmt.Errorf("restore previous output from %s: %w", backup, restoreErr))
		}
		return err
	}
	return os.RemoveAll(backup)
}

func execute(ctx context.Context, root, mode string, run generator) error {
	if mode != "generate" && mode != "check" {
		return errors.New("usage: sqlgen [-go executable] generate|check")
	}
	want, err := generate(ctx, root, run)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, outputDir)
	actual, err := readFiles(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	diff := differences(want, actual)
	if len(diff) == 0 {
		return nil
	}
	if mode == "check" {
		return fmt.Errorf("generated SQL source is stale; run make generate:\n%s", strings.Join(diff, "\n"))
	}
	return replaceOutput(dir, want)
}

func runSQLC(goCommand string) generator {
	return func(ctx context.Context, dir string) error {
		command, err := exec.LookPath(goCommand)
		if err != nil {
			return err
		}
		command, err = filepath.Abs(command)
		if err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, command, "run", sqlcModule, "generate")
		cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
		// The isolated project must not inherit a caller's Go workspace.
		cmd.Env = append(os.Environ(), "GOWORK=off")
		return cmd.Run()
	}
}

func main() {
	goCommand := flag.String("go", "go", "Go executable used to run the pinned sqlc version")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: sqlgen [-go executable] generate|check")
		os.Exit(1)
	}
	root, err := os.Getwd()
	if err == nil {
		err = execute(context.Background(), root, flag.Arg(0), runSQLC(*goCommand))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
