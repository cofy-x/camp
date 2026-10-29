package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type invocation struct {
	Args      []string
	Directory string
	Workspace string
}

func TestMain(m *testing.M) {
	if capture := os.Getenv("PLATINUM_SQLGEN_TEST_CAPTURE"); capture != "" {
		directory, err := os.Getwd()
		if err != nil {
			os.Exit(1)
		}
		data, err := json.Marshal(invocation{os.Args[1:], directory, os.Getenv("GOWORK")})
		if err != nil || os.WriteFile(capture, data, 0o600) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRunSQLCUsesSelectedGoAndIsolatedWorkspace(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(t.TempDir(), "invocation.json")
	t.Setenv("PLATINUM_SQLGEN_TEST_CAPTURE", capture)
	t.Setenv("GOWORK", "unrelated-workspace")
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	executable, err := filepath.Rel(workingDir, os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := runSQLC(executable)(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var got invocation
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	wantDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Args, []string{"run", sqlcModule, "generate"}) || got.Directory != wantDir || got.Workspace != "off" {
		t.Fatalf("unexpected Go invocation: %+v", got)
	}
}

const testConfig = `version: '2'
sql:
  - engine: sqlite
    schema: apps/server/internal/storage/migrations
    queries: apps/server/internal/storage/queries
    gen:
      go:
        package: sqlgen
        out: apps/server/internal/storage/internal/sqlgen
        sql_package: database/sql
        emit_empty_slices: true
`

func testProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := writeFiles(root, map[string][]byte{
		"sqlc.yaml":                 []byte(testConfig),
		schemaDir + "/00001.sql":    []byte("CREATE TABLE entries(id TEXT);"),
		queriesDir + "/entries.sql": []byte("-- name: List :many\nSELECT id FROM entries;"),
	}); err != nil {
		t.Fatal(err)
	}
	return root
}

func fakeGenerator(t *testing.T, root string, output map[string][]byte) generator {
	t.Helper()
	return func(_ context.Context, dir string) error {
		if dir == root {
			t.Fatal("generator must run outside the source project")
		}
		for _, name := range []string{"sqlc.yaml", schemaDir + "/00001.sql", queriesDir + "/entries.sql"} {
			want, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || string(got) != string(want) {
				t.Fatalf("temporary input %s = %q, want %q: %v", name, got, want, err)
			}
		}
		return writeFiles(filepath.Join(dir, outputDir), output)
	}
}

func TestCheckComparesCompleteFileSetAndContent(t *testing.T) {
	want := map[string][]byte{"db.go": []byte("database"), "entries.sql.go": []byte("queries")}
	for _, tc := range []struct {
		name   string
		actual map[string][]byte
		want   string
	}{
		{"same", want, ""},
		{"changed", map[string][]byte{"db.go": []byte("edited"), "entries.sql.go": []byte("queries")}, "changed db.go"},
		{"missing", map[string][]byte{"db.go": []byte("database")}, "missing entries.sql.go"},
		{"stale", map[string][]byte{"db.go": []byte("database"), "entries.sql.go": []byte("queries"), "deleted.sql.go": []byte("stale query")}, "unexpected deleted.sql.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := testProject(t)
			if err := writeFiles(filepath.Join(root, outputDir), tc.actual); err != nil {
				t.Fatal(err)
			}
			err := execute(context.Background(), root, "check", fakeGenerator(t, root, want))
			if tc.want == "" && err != nil {
				t.Fatal(err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			actual, err := readFiles(filepath.Join(root, outputDir))
			if err != nil || !reflect.DeepEqual(actual, tc.actual) {
				t.Fatalf("check modified source output: %v", err)
			}
		})
	}
}

func TestGenerateReplacesExactFileSet(t *testing.T) {
	root := testProject(t)
	dir := filepath.Join(root, outputDir)
	if err := writeFiles(dir, map[string][]byte{"db.go": []byte("old"), "deleted.sql.go": []byte("stale")}); err != nil {
		t.Fatal(err)
	}
	want := map[string][]byte{"db.go": []byte("new"), "entries.sql.go": []byte("new query")}
	if err := execute(context.Background(), root, "generate", fakeGenerator(t, root, want)); err != nil {
		t.Fatal(err)
	}
	actual, err := readFiles(dir)
	if err != nil || !reflect.DeepEqual(actual, want) {
		t.Fatalf("output = %v, want %v: %v", actual, want, err)
	}
}

func TestFailedGenerationPreservesCurrentOutput(t *testing.T) {
	root := testProject(t)
	dir := filepath.Join(root, outputDir)
	want := map[string][]byte{"db.go": []byte("keep"), "entries.sql.go": []byte("keep queries")}
	if err := writeFiles(dir, want); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected sqlc failure")
	err := execute(context.Background(), root, "generate", func(_ context.Context, dir string) error {
		if err := writeFiles(filepath.Join(dir, outputDir), map[string][]byte{"db.go": []byte("incomplete")}); err != nil {
			t.Fatal(err)
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("want generator failure, got %v", err)
	}
	actual, err := readFiles(dir)
	if err != nil || !reflect.DeepEqual(actual, want) {
		t.Fatalf("failed generator modified current output: %v", err)
	}
}

func TestRejectUnsafeOrUnsupportedConfigBeforeRunning(t *testing.T) {
	for _, data := range []string{
		strings.Replace(testConfig, "out: "+outputDir, "out: /tmp/escaped", 1),
		strings.Replace(testConfig, "out: "+outputDir, "out: ../escaped\n        # out: "+outputDir, 1),
		strings.Replace(testConfig, "out: "+outputDir, "out: "+outputDir+"\n        out: ../escaped", 1),
		strings.Replace(testConfig, "schema: "+schemaDir, "schema: ../escaped", 1),
		strings.Replace(testConfig, "queries: "+queriesDir, "queries: /tmp/escaped", 1),
		strings.Replace(testConfig, "engine: sqlite", "engine: sqlite\n    unknown: true", 1),
		strings.Replace(testConfig, "emit_empty_slices: true", "emit_empty_slices: true\n  - engine: sqlite", 1),
		testConfig + "---\nversion: '2'\n",
	} {
		t.Run(data, func(t *testing.T) {
			root := testProject(t)
			if err := os.WriteFile(filepath.Join(root, "sqlc.yaml"), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
			err := execute(context.Background(), root, "generate", func(context.Context, string) error {
				t.Fatal("unsupported configuration reached the generator")
				return nil
			})
			if err == nil {
				t.Fatal("accepted unsupported configuration")
			}
		})
	}
}
