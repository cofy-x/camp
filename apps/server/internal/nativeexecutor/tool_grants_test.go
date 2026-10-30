package nativeexecutor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestToolGrantsResolveAliasesAndProtectPrivateStorage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tool := filepath.Join(home, "tools", "compiler")
	private := filepath.Join(home, "camp", "runner")
	if err := os.MkdirAll(tool, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "compiler-current")
	if err := os.Symlink(tool, alias); err != nil {
		t.Fatal(err)
	}
	roots, err := ResolveToolRoots(private, []string{alias, tool})
	canonical, _ := filepath.EvalSymlinks(tool)
	if err != nil || !reflect.DeepEqual(roots, []string{canonical}) {
		t.Fatal("grants did not retain one canonical tool root", roots, err)
	}
	for _, bad := range []string{home, filepath.Dir(home), filepath.Join(home, "camp"), "/", "relative"} {
		if _, err := ResolveToolRoots(private, []string{bad}); err == nil {
			t.Fatal("broad or private ancestor grant was accepted", bad)
		}
	}
	for _, secret := range []string{private, filepath.Join(home, ".ssh"), filepath.Join(home, ".azure"), filepath.Join(home, ".docker"), filepath.Join(home, ".kube"), filepath.Join(home, "Library", "Keychains")} {
		if err := os.MkdirAll(filepath.Join(secret, "child"), 0700); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{secret, filepath.Join(secret, "child")} {
			if _, err := ResolveToolRoots(private, []string{bad}); err == nil {
				t.Fatal("private tool grant was accepted", bad)
			}
		}
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveToolRoots(private, []string{alias}); err == nil {
		t.Fatal("an alias exposed Runner storage")
	}
}
