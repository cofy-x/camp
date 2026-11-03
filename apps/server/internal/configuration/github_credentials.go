package configuration

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// GitHubCredentials retains immutable outbound credentials outside team SQLite.
// The reference can be stored with a policy; the token never belongs in its JSON.
type GitHubCredentials struct{ directory string }

type GitHubCredential struct {
	Token   string `json:"-"`
	ActorID int64  `json:"actorId"`
}

func OpenGitHubCredentials(directory string) (*GitHubCredentials, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("GitHub credentials require a private directory")
	}
	if err = os.Chmod(directory, 0700); err != nil {
		return nil, err
	}
	return &GitHubCredentials{directory: directory}, nil
}

func validGitHubCredential(token string, actorID int64) bool {
	return actorID > 0 && len(token) >= 20 && len(token) <= 1024 && utf8.ValidString(token) &&
		strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

func (s *GitHubCredentials) Write(token string, actorID int64) (string, error) {
	if !validGitHubCredential(token, actorID) {
		return "", &Error{Code: "invalid_credential", Message: "Provide a GitHub token and its positive numeric account ID."}
	}
	var identity [24]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return "", err
	}
	ref := hex.EncodeToString(identity[:])
	raw, err := json.Marshal(struct {
		Token   string `json:"token"`
		ActorID int64  `json:"actorId"`
	}{token, actorID})
	if err != nil {
		return "", err
	}
	if err = writePrivate(filepath.Join(s.directory, ref+".json"), raw); err != nil {
		return "", err
	}
	return ref, nil
}

func (s *GitHubCredentials) Read(ref string) (GitHubCredential, error) {
	if len(ref) != 48 {
		return GitHubCredential{}, errors.New("Invalid GitHub credential reference")
	}
	if _, err := hex.DecodeString(ref); err != nil {
		return GitHubCredential{}, errors.New("Invalid GitHub credential reference")
	}
	var value struct {
		Token   string `json:"token"`
		ActorID int64  `json:"actorId"`
	}
	if err := readPrivate(filepath.Join(s.directory, ref+".json"), &value); err != nil {
		return GitHubCredential{}, errors.New("The private GitHub credential is unavailable")
	}
	if !validGitHubCredential(value.Token, value.ActorID) {
		return GitHubCredential{}, errors.New("The private GitHub credential is invalid")
	}
	return GitHubCredential{Token: value.Token, ActorID: value.ActorID}, nil
}
