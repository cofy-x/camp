package resources

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Use Git's matcher rather than another glob implementation. Only ignore files
// along candidate ancestors are materialized; originals are never changed.
func ignoredDeliveryPaths(ctx context.Context, repository string, entries map[string]treeEntry, names []string) (map[string]bool, error) {
	ignored := map[string]bool{}
	if len(names) == 0 {
		return ignored, nil
	}
	rules := map[string]treeEntry{}
	for _, name := range names {
		if !validPath(name) {
			return nil, errors.New("The delivery contains an unsafe file path")
		}
		for dir := path.Dir(name); ; dir = path.Dir(dir) {
			rule := path.Join(dir, ".gitignore")
			if entry, ok := entries[rule]; ok {
				rules[rule] = entry
			}
			if dir == "." {
				break
			}
		}
	}
	if len(rules) == 0 {
		return ignored, nil
	}
	directory, err := os.MkdirTemp(filepath.Dir(repository), ".delivery-ignore-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	total := 0
	if err = visitEntries(ctx, repository, rules, func(name string, _ treeEntry, content []byte) error {
		total += len(content)
		if total > 2<<20 {
			return errors.New("The relevant project ignore rules exceed 2 MiB")
		}
		target := filepath.Join(directory, filepath.FromSlash(name))
		if e := os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return e
		}
		return os.WriteFile(target, content, 0600)
	}); err != nil {
		return nil, err
	}
	command, commandContext, cancel := gitCommand(ctx, repository, []byte(strings.Join(names, "\x00")+"\x00"), "--work-tree="+directory, "-c", "core.excludesFile="+os.DevNull, "-c", "core.ignoreCase=false", "check-ignore", "--no-index", "-z", "--stdin")
	defer cancel()
	output, err := command.Output()
	if commandContext.Err() != nil {
		return nil, commandContext.Err()
	}
	if err != nil {
		if command.ProcessState == nil || command.ProcessState.ExitCode() != 1 {
			return nil, errors.New("The accepted project ignore rules could not be evaluated")
		}
	}
	for _, name := range strings.Split(string(output), "\x00") {
		if name != "" {
			ignored[name] = true
		}
	}
	return ignored, nil
}
