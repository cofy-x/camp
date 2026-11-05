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

// FeishuCredentials retains immutable webhook key/token pairs outside team data.
type FeishuCredentials struct{ directory string }
type FeishuCredential struct {
	VerificationToken string `json:"-"`
	EncryptKey        string `json:"-"`
}

func OpenFeishuCredentials(directory string) (*FeishuCredentials, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Feishu credentials require a private directory")
	}
	if err = os.Chmod(directory, 0700); err != nil {
		return nil, err
	}
	return &FeishuCredentials{directory: directory}, nil
}
func validFeishuSecret(value string) bool {
	return len(value) >= 16 && len(value) <= 256 && utf8.ValidString(value) && strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}
func (s *FeishuCredentials) Write(token, key string) (string, error) {
	if !validFeishuSecret(token) || !validFeishuSecret(key) {
		return "", &Error{Code: "invalid_source", Message: "Provide the Feishu Verification Token and Encrypt Key (16 to 256 bytes each)."}
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	ref := hex.EncodeToString(random[:])
	raw, err := json.Marshal(struct {
		Token string `json:"verificationToken"`
		Key   string `json:"encryptKey"`
	}{token, key})
	if err != nil {
		return "", err
	}
	if err = writePrivate(filepath.Join(s.directory, ref+".json"), raw); err != nil {
		return "", err
	}
	return ref, nil
}
func (s *FeishuCredentials) Read(ref string) (FeishuCredential, error) {
	if len(ref) != 48 {
		return FeishuCredential{}, errors.New("Invalid Feishu credential reference")
	}
	if _, err := hex.DecodeString(ref); err != nil {
		return FeishuCredential{}, errors.New("Invalid Feishu credential reference")
	}
	var value struct {
		Token string `json:"verificationToken"`
		Key   string `json:"encryptKey"`
	}
	if err := readPrivate(filepath.Join(s.directory, ref+".json"), &value); err != nil {
		return FeishuCredential{}, errors.New("The private Feishu credential is unavailable")
	}
	if !validFeishuSecret(value.Token) || !validFeishuSecret(value.Key) {
		return FeishuCredential{}, errors.New("The private Feishu credential is invalid")
	}
	return FeishuCredential{VerificationToken: value.Token, EncryptKey: value.Key}, nil
}
