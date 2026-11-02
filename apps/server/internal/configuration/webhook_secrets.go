package configuration

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// WebhookSecrets keeps immutable credentials outside the team database. SQLite
// stores only an opaque reference; a rotation persists its new file first.
type WebhookSecrets struct{ directory string }

func OpenWebhookSecrets(directory string) (*WebhookSecrets, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Webhook credentials need a private directory")
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, err
	}
	return &WebhookSecrets{directory: directory}, nil
}

func (s *WebhookSecrets) Put(secret string) (string, error) {
	if len(secret) < 32 || len(secret) > 256 || strings.TrimSpace(secret) != secret || strings.ContainsAny(secret, "\x00\r\n") {
		return "", &Error{Code: "invalid_source", Message: "Use a random webhook secret of 32 to 256 bytes."}
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	ref := hex.EncodeToString(random[:])
	raw, _ := json.Marshal(struct {
		Secret string `json:"secret"`
	}{secret})
	// The random name is never reused. A failed SQLite transaction may leave an
	// unreferenced private credential; it cannot activate a source.
	if err := writePrivate(filepath.Join(s.directory, ref+".json"), raw); err != nil {
		return "", err
	}
	return ref, nil
}

func (s *WebhookSecrets) Get(ref string) (string, error) {
	if len(ref) != 48 {
		return "", errors.New("Invalid webhook credential reference")
	}
	if _, err := hex.DecodeString(ref); err != nil {
		return "", errors.New("Invalid webhook credential reference")
	}
	var value struct {
		Secret string `json:"secret"`
	}
	if err := readPrivate(filepath.Join(s.directory, ref+".json"), &value); err != nil {
		return "", err
	}
	if len(value.Secret) < 32 || len(value.Secret) > 256 {
		return "", errors.New("Invalid private webhook credential")
	}
	return value.Secret, nil
}
