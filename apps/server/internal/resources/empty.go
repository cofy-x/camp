package resources

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// EmptyRevision is the Git object ID of emptyCommitBody. Every automatic empty
// resource starts at this real revision, so startup and empty file browsing do
// not need Git installed. The first captured result materializes its parent.
const EmptyRevision = "b88cb406468b9a60fe09616948d438fb8f1ad023"
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
const emptyCommitBody = "tree " + emptyTree + "\nauthor Camp <camp@localhost> 0 +0000\ncommitter Camp <camp@localhost> 0 +0000\n\nEmpty resource\n"
const emptyResourceMarker = "camp-empty-resource"

func checkEmptyResource(repository string) error {
	info, err := os.Lstat(repository)
	if err != nil || !info.IsDir() {
		return errors.New("The empty resource directory does not exist")
	}
	marker := filepath.Join(repository, emptyResourceMarker)
	info, err = os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(EmptyRevision)+1) {
		return errors.New("This resource does not own the empty revision")
	}
	body, err := os.ReadFile(marker)
	if err != nil || string(body) != EmptyRevision+"\n" {
		return errors.New("The empty resource marker is invalid")
	}
	return nil
}

// Reinitializing a partial bare repository is intentional: a crash may occur
// after any plumbing command. No previously saved objects or refs are removed.
func materializeEmptyResource(ctx context.Context, repository string) error {
	if err := checkEmptyResource(repository); err != nil {
		return err
	}
	if err := checkCommit(ctx, repository, EmptyRevision); err == nil {
		return nil
	}
	if _, err := git(ctx, repository, nil, "init", "--bare", "--template=", "--object-format=sha1", "--initial-branch=main", repository); err != nil {
		return err
	}
	tree, err := git(ctx, repository, nil, "mktree")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(tree)) != emptyTree {
		return errors.New("Git returned an unexpected empty tree")
	}
	commit, err := git(ctx, repository, []byte(emptyCommitBody), "hash-object", "-w", "-t", "commit", "--stdin")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(commit)) != EmptyRevision {
		return errors.New("Git returned an unexpected empty revision")
	}
	_, err = git(ctx, repository, nil, "update-ref", "refs/camp/snapshots/"+EmptyRevision, EmptyRevision)
	return err
}
