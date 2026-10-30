//go:build darwin

package resources

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeMaterializationNeverMergesPathAliases(t *testing.T) {
	for _, pair := range [][2]string{{"Case", "case"}, {"\u00e9", "e\u0301"}} {
		t.Run(pair[0], func(t *testing.T) {
			probe := t.TempDir()
			if err := os.Mkdir(filepath.Join(probe, pair[0]), 0700); err != nil {
				t.Fatal(err)
			}
			_, aliasErr := os.Stat(filepath.Join(probe, pair[1]))
			aliased := aliasErr == nil
			t.Logf("filesystem aliases %q and %q: %v", pair[0], pair[1], aliased)
			files := snapshot{pair[0] + "/first.txt": file{content: []byte("first")}, pair[1] + "/second.txt": file{content: []byte("second")}}
			err := writeSnapshot(filepath.Join(t.TempDir(), "project"), files)
			if (err != nil) != aliased {
				t.Fatalf("path preservation: alias=%v error=%v", aliased, err)
			}
		})
	}
}
