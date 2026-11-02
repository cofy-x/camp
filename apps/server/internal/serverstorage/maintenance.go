package serverstorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
)

const maintenanceRoot = ".storage-maintenance"

type File struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Mode      uint32 `json:"mode"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256,omitempty"`
}
type Entry struct {
	Path  string `json:"path"`
	Files []File `json:"files"`
}
type Manifest struct {
	Version         int     `json:"version"`
	Identity        string  `json:"identity"`
	ReferenceDigest string  `json:"referenceDigest"`
	CreatedAt       string  `json:"createdAt"`
	Entries         []Entry `json:"entries"`
}
type State struct {
	ID        string   `json:"id"`
	Status    string   `json:"status"`
	Completed []string `json:"completed"`
	UpdatedAt string   `json:"updatedAt"`
}

func (s *session) eligible(name string) bool {
	value := s.retention(name)
	return value == "orphan" || value == "terminal-transfer"
}
func snapshotTree(ctx context.Context, root *os.Root, name string) ([]File, error) {
	remaining := 10000
	var pathBytes int
	var total int64
	files := []File{}
	err := walk(ctx, root, name, 0, &remaining, func(current string, info os.FileInfo) error {
		relative := strings.TrimPrefix(strings.TrimPrefix(current, name), "/")
		if relative == "" {
			relative = "."
		}
		pathBytes += len(relative)
		if pathBytes > 8<<20 {
			return errors.New("An orphan object exceeds its path metadata budget")
		}
		item := File{Path: relative, Directory: info.IsDir(), Mode: uint32(info.Mode().Perm()), Bytes: info.Size()}
		if info.IsDir() {
			item.Bytes = 0
		} else {
			total += info.Size()
			if total > MaxObjectBytes {
				return errors.New("An orphan object exceeds its 512 MiB plan budget")
			}
			hash, err := hashRegular(ctx, root, current, info)
			if err != nil {
				return err
			}
			item.SHA256 = hash
		}
		files = append(files, item)
		return nil
	})
	return files, err
}
func Plan(ctx context.Context, directory string, limit int) (State, error) {
	if limit < 1 || limit > MaxPlanObjects {
		return State{}, errors.New("Plan from 1 to 50 orphan objects at a time")
	}
	s, err := open(ctx, directory)
	if err != nil {
		return State{}, err
	}
	defer s.close()
	report, err := s.inspect(ctx)
	if err != nil {
		return State{}, err
	}
	// Reserve the plan/receipt and quarantine parent entries. A valid plan
	// must not consume the headroom its own recovery inventory needs.
	if report.Entries > MaxEntries-16 {
		return State{}, errors.New("The inventory has no maintenance recovery headroom")
	}
	manifest := Manifest{Version: 1, Identity: s.catalog.Identity, ReferenceDigest: s.digest, CreatedAt: now(), Entries: []Entry{}}
	var plannedBytes int64
	for _, object := range report.Objects {
		if !s.eligible(object.Path) {
			continue
		}
		if len(manifest.Entries) > 0 && plannedBytes+object.Bytes > MaxObjectBytes {
			break
		}
		plannedBytes += object.Bytes
		files, e := snapshotTree(ctx, s.root, object.Path)
		if e != nil {
			return State{}, e
		}
		manifest.Entries = append(manifest.Entries, Entry{Path: object.Path, Files: files})
		if len(manifest.Entries) == limit {
			break
		}
	}
	if len(manifest.Entries) == 0 {
		return State{}, errors.New("No unreferenced Server objects are eligible for quarantine")
	}
	id, data, err := fingerprint(manifest)
	if err != nil {
		return State{}, err
	}
	if len(data) > 32<<20 {
		return State{}, errors.New("The maintenance manifest exceeds its 32 MiB budget")
	}
	base := maintenanceRoot + "/plans/" + id
	if err = s.safeMkdir(base); err != nil {
		return State{}, err
	}
	if err = s.atomicWrite(base+"/manifest.json", data); err != nil {
		return State{}, err
	}
	state := State{ID: id, Status: "planned", Completed: []string{}, UpdatedAt: now()}
	if err = s.saveState(state); err != nil {
		return State{}, err
	}
	return state, nil
}
func (s *session) load(id string) (Manifest, State, error) {
	var manifest Manifest
	var state State
	if !planID.MatchString(id) {
		return manifest, state, errors.New("Choose an exact maintenance plan ID")
	}
	base := maintenanceRoot + "/plans/" + id
	data, err := s.readRegular(base+"/manifest.json", 32<<20)
	if err != nil {
		return manifest, state, err
	}
	if err = decode(data, &manifest); err != nil {
		return manifest, state, err
	}
	digest, _, err := fingerprint(manifest)
	if err != nil || digest != id || manifest.Version != 1 || manifest.Identity != s.catalog.Identity || len(manifest.Entries) == 0 || len(manifest.Entries) > MaxPlanObjects {
		return manifest, state, errors.New("The maintenance manifest or Server identity changed")
	}
	seen := map[string]bool{}
	for _, entry := range manifest.Entries {
		parts := strings.Split(entry.Path, "/")
		if len(parts) != 2 || !slices.Contains([]string{"resources", "remote"}, parts[0]) || !objectID.MatchString(parts[1]) || seen[entry.Path] || len(entry.Files) == 0 || len(entry.Files) > 10000 {
			return manifest, state, errors.New("The maintenance manifest contains an invalid object")
		}
		seen[entry.Path] = true
		names := map[string]bool{}
		for i, file := range entry.Files {
			if file.Path != "." && (path.Clean(file.Path) != file.Path || path.IsAbs(file.Path) || strings.HasPrefix(file.Path, "../") || strings.ContainsAny(file.Path, "\\\x00")) {
				return manifest, state, errors.New("A maintenance file path is unsafe")
			}
			if names[file.Path] || file.Mode > 0777 || file.Bytes < 0 || (i == 0 && file.Path != ".") || (!file.Directory && !planID.MatchString(file.SHA256)) {
				return manifest, state, errors.New("A maintenance file record is invalid")
			}
			names[file.Path] = true
		}
	}
	data, err = s.readRegular(base+"/state.json", 1<<20)
	if err != nil {
		return manifest, state, err
	}
	if err = decode(data, &state); err != nil {
		return manifest, state, err
	}
	if state.ID != id || !slices.Contains([]string{"planned", "quarantining", "quarantined", "restoring", "restored", "purging", "purged", "cancelled"}, state.Status) {
		return manifest, state, errors.New("The maintenance receipt is invalid")
	}
	for _, name := range state.Completed {
		if !seen[name] {
			return manifest, state, errors.New("The maintenance receipt names an unknown object")
		}
	}
	return manifest, state, nil
}
func Status(ctx context.Context, directory, id string) ([]State, error) {
	s, err := open(ctx, directory)
	if err != nil {
		return nil, err
	}
	defer s.close()
	if id != "" {
		_, state, err := s.load(id)
		return []State{state}, err
	}
	base := maintenanceRoot + "/plans"
	info, err := s.root.Lstat(base)
	if errors.Is(err, os.ErrNotExist) {
		return []State{}, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("The maintenance plan directory is invalid")
	}
	directoryFile, err := s.root.Open(base)
	if err != nil {
		return nil, err
	}
	defer directoryFile.Close()
	entries, err := directoryFile.ReadDir(1001)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > 1000 {
		return nil, errors.New("Read an exact plan ID; the plan listing exceeds its 1000-plan budget")
	}
	states := []State{}
	for _, entry := range entries {
		if !entry.IsDir() || !planID.MatchString(entry.Name()) {
			return nil, errors.New("The maintenance plan directory has an unknown entry")
		}
		_, state, e := s.load(entry.Name())
		if e != nil {
			return nil, e
		}
		states = append(states, state)
	}
	slices.SortFunc(states, func(a, b State) int { return strings.Compare(a.ID, b.ID) })
	return states, nil
}
func Apply(ctx context.Context, directory, id, action string, permanent bool) (State, error) {
	if !slices.Contains([]string{"quarantine", "restore", "cancel", "purge"}, action) {
		return State{}, errors.New("Unknown storage maintenance action")
	}
	if action == "purge" && !permanent {
		return State{}, errors.New("Permanent purge requires --confirm-permanent for this exact plan")
	}
	if action != "purge" && permanent {
		return State{}, errors.New("Permanent confirmation is accepted only for purge")
	}
	s, err := open(ctx, directory)
	if err != nil {
		return State{}, err
	}
	defer s.close()
	// Incomplete or linked inventories never authorize mutations, even if the
	// selected object looks unrelated to the unsupported data.
	if _, err = s.inspect(ctx); err != nil {
		return State{}, err
	}
	manifest, state, err := s.load(id)
	if err != nil {
		return state, err
	}
	complete := func(status string) (State, error) {
		state.Status = status
		state.UpdatedAt = now()
		err := s.saveState(state)
		return state, err
	}
	terminalAction := map[string]string{"purged": "purge", "restored": "restore", "cancelled": "cancel"}
	if completed, ok := terminalAction[state.Status]; ok {
		if action != completed {
			return state, errors.New("The maintenance action is terminal; create a new plan for another action")
		}
		return state, s.saveState(state)
	}
	switch action {
	case "quarantine":
		if state.Status == "quarantined" {
			return state, s.saveState(state)
		}
		if state.Status != "planned" && state.Status != "quarantining" {
			return state, errors.New("This maintenance plan cannot quarantine")
		}
		if manifest.ReferenceDigest != s.digest {
			return state, errors.New("The reference snapshot changed; restore/cancel any partial quarantine and create a new plan")
		}
		state.Status = "quarantining"
	case "restore", "cancel":
		if state.Status == "purging" {
			return state, errors.New("A dispatched permanent purge cannot be cancelled or restored")
		}
		if state.Status != "restoring" {
			state.Completed = []string{}
		}
		state.Status = "restoring"
	case "purge":
		if state.Status != "quarantined" && state.Status != "purging" {
			return state, errors.New("Quarantine all objects before authorizing permanent purge")
		}
	}
	for _, entry := range manifest.Entries {
		if !s.eligible(entry.Path) {
			return state, errors.New("A maintenance object is now referenced or active; its bytes are protected")
		}
	}
	// Intent is durable before any rename or unlink. Exact retries reconcile
	// source/destination and expected bytes rather than replaying blind deletion.
	if action == "purge" && state.Status != "purging" {
		for _, entry := range manifest.Entries {
			if err = s.matches(ctx, quarantined(id, entry.Path), entry.Files, false); err != nil {
				return state, err
			}
		}
		state.Status = "purging"
		state.Completed = []string{}
	}
	state.UpdatedAt = now()
	if err = s.saveState(state); err != nil {
		return state, err
	}
	for _, entry := range manifest.Entries {
		if err = ctx.Err(); err != nil {
			return state, err
		}
		if err = s.unchanged(); err != nil {
			return state, err
		}
		destination := quarantined(id, entry.Path)
		switch action {
		case "quarantine":
			err = s.move(ctx, entry.Path, destination, entry.Files)
		case "restore", "cancel":
			err = s.move(ctx, destination, entry.Path, entry.Files)
		case "purge":
			err = s.purge(ctx, destination, entry.Files)
		}
		if err != nil {
			return state, err
		}
		if !slices.Contains(state.Completed, entry.Path) {
			state.Completed = append(state.Completed, entry.Path)
		}
		state.UpdatedAt = now()
		if err = s.saveState(state); err != nil {
			return state, err
		}
	}
	switch action {
	case "quarantine":
		return complete("quarantined")
	case "restore":
		return complete("restored")
	case "cancel":
		return complete("cancelled")
	default:
		return complete("purged")
	}
}
func quarantined(id, name string) string { return maintenanceRoot + "/quarantine/" + id + "/" + name }
func (s *session) move(ctx context.Context, from, to string, expected []File) error {
	source, sourceErr := s.root.Lstat(from)
	target, targetErr := s.root.Lstat(to)
	_ = source
	_ = target
	if sourceErr == nil && targetErr == nil {
		return errors.New("Both maintenance locations contain bytes; nothing was overwritten")
	}
	if errors.Is(sourceErr, os.ErrNotExist) && targetErr == nil {
		if err := s.matches(ctx, to, expected, false); err != nil {
			return err
		}
		// Cancelling a plan before its first move leaves no quarantine parent.
		// A completed move still syncs both parents when they exist.
		sourceSync := s.syncDirectory(path.Dir(from))
		if errors.Is(sourceSync, os.ErrNotExist) {
			sourceSync = nil
		}
		return errors.Join(sourceSync, s.syncDirectory(path.Dir(to)))
	}
	if sourceErr != nil {
		return sourceErr
	}
	if !errors.Is(targetErr, os.ErrNotExist) {
		return targetErr
	}
	if err := s.matches(ctx, from, expected, false); err != nil {
		return err
	}
	if err := s.safeMkdir(path.Dir(to)); err != nil {
		return err
	}
	if err := s.root.Rename(from, to); err != nil {
		return err
	}
	return errors.Join(s.syncDirectory(path.Dir(from)), s.syncDirectory(path.Dir(to)))
}
func (s *session) matches(ctx context.Context, name string, expected []File, missing bool) error {
	if missing {
		if _, err := s.root.Lstat(name); errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	actual, err := snapshotTree(ctx, s.root, name)
	if err != nil {
		return err
	}
	allowed := map[string]File{}
	for _, file := range expected {
		allowed[file.Path] = file
	}
	for _, file := range actual {
		before, ok := allowed[file.Path]
		if !ok || file != before {
			return errors.New("A maintenance object changed; unexpected bytes were retained")
		}
		delete(allowed, file.Path)
	}
	if !missing && len(allowed) != 0 {
		return errors.New("A maintenance object is incomplete")
	}
	return nil
}
func (s *session) purge(ctx context.Context, name string, expected []File) error {
	if err := s.matches(ctx, name, expected, true); err != nil {
		return err
	}
	for i := len(expected) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		file := expected[i]
		relative := name
		if file.Path != "." {
			relative += "/" + file.Path
		}
		info, err := s.root.Lstat(relative)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != file.Directory || uint32(info.Mode().Perm()) != file.Mode {
			return errors.New("A purge entry changed type or mode")
		}
		if !file.Directory {
			hash, e := hashRegular(ctx, s.root, relative, info)
			if e != nil || hash != file.SHA256 {
				return errors.New("A purge entry changed content")
			}
		}
		if err = s.root.Remove(relative); err != nil {
			return err
		}
		if err = s.syncDirectory(path.Dir(relative)); err != nil {
			return err
		}
	}
	return s.syncDirectory(path.Dir(name))
}
func (s *session) saveState(state State) error {
	_, data, err := fingerprint(state)
	if err != nil {
		return err
	}
	return s.atomicWrite(maintenanceRoot+"/plans/"+state.ID+"/state.json", data)
}
func (s *session) safeMkdir(name string) error {
	current := ""
	for _, part := range strings.Split(name, "/") {
		if part == "." || part == "" || part == ".." {
			return errors.New("Invalid maintenance directory")
		}
		if current != "" {
			current += "/"
		}
		current += part
		info, err := s.root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err = s.root.Mkdir(current, 0700); err != nil {
				return err
			}
			if err = s.syncDirectory(path.Dir(current)); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("A maintenance directory was replaced")
		}
	}
	return nil
}
func (s *session) syncDirectory(name string) error {
	file, err := s.root.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
func (s *session) atomicWrite(name string, data []byte) error {
	if err := s.safeMkdir(path.Dir(name)); err != nil {
		return err
	}
	temporary := path.Dir(name) + "/.pending-" + randomID()
	file, err := s.root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer s.root.Remove(temporary)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if err = s.root.Rename(temporary, name); err != nil {
		return err
	}
	return s.syncDirectory(path.Dir(name))
}
func (s *session) readRegular(name string, limit int64) ([]byte, error) {
	info, err := s.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("Unsupported maintenance record: %s", name)
	}
	file, err := s.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("A maintenance record changed during read")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("A maintenance record exceeds its budget")
	}
	return data, nil
}
