package resources

import (
	"context"
	"errors"
	"slices"
	"strings"
)

// Change contains only the exact accepted patch, not a checkout or capability.
type Change struct {
	Path    string
	OldSHA  string
	OldMode string
	SHA     string
	Mode    string
	Content []byte
}

// Changes compares Git trees first, then reads changed blobs in one bounded
// batch. Unchanged repository files and executable bits remain authoritative.
func (m *Manager) Changes(ctx context.Context, id, base, result string, trackedPaths []string) (out []Change, err error) {
	repository, err := m.repository(id)
	if err != nil {
		return nil, err
	}
	before, err := listCommit(ctx, repository, base)
	if err != nil {
		return nil, err
	}
	after, err := listCommit(ctx, repository, result)
	if err != nil {
		return nil, err
	}
	changed := map[string]treeEntry{}
	out = []Change{}
	for name, entry := range before {
		if _, ok := after[name]; !ok {
			out = append(out, Change{Path: name, OldSHA: entry.object, OldMode: modeForEntry(entry), Mode: "100644"})
		}
	}
	for name, entry := range after {
		old, ok := before[name]
		if !ok || old != entry {
			changed[name] = entry
		}
	}
	// Publish source under the accepted project's ignore rules. Existing target
	// files stay tracked even if an ignore rule now matches their name.
	tracked := map[string]bool{}
	for _, name := range trackedPaths {
		tracked[name] = true
	}
	candidates := []string{}
	for _, entry := range out {
		if !tracked[entry.Path] {
			candidates = append(candidates, entry.Path)
		}
	}
	for name := range changed {
		if !tracked[name] {
			candidates = append(candidates, name)
		}
	}
	ignored, err := ignoredDeliveryPaths(ctx, repository, after, candidates)
	if err != nil {
		return nil, err
	}
	kept := out[:0]
	for _, entry := range out {
		if !ignored[entry.Path] {
			kept = append(kept, entry)
		}
	}
	out = kept
	for name := range ignored {
		delete(changed, name)
	}
	if len(out)+len(changed) == 0 {
		return nil, errors.New("The accepted result has no source changes to deliver")
	}
	if len(out)+len(changed) > 128 {
		return nil, errors.New("A delivery supports at most 128 changed files")
	}
	for name := range changed {
		if strings.HasPrefix(strings.ToLower(name), ".github/") {
			return nil, errors.New("This delivery does not authorize GitHub workflow or repository configuration changes")
		}
	}
	for _, entry := range out {
		if strings.HasPrefix(strings.ToLower(entry.Path), ".github/") {
			return nil, errors.New("This delivery does not authorize GitHub workflow or repository configuration changes")
		}
	}
	bytes := 0
	err = visitEntries(ctx, repository, changed, func(name string, entry treeEntry, content []byte) error {
		bytes += len(content)
		if bytes > 8<<20 {
			return errors.New("The accepted delivery patch exceeds 8 MiB")
		}
		mode := "100644"
		if entry.executable {
			mode = "100755"
		}
		out = append(out, Change{Path: name, OldSHA: before[name].object, OldMode: modeForEntry(before[name]), SHA: entry.object, Mode: mode, Content: content})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

func modeForEntry(entry treeEntry) string {
	if entry.object == "" {
		return ""
	}
	if entry.executable {
		return "100755"
	}
	return "100644"
}
