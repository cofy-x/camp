package team

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const passwordMemory = 64 * 1024
const passwordIterations = 3
const passwordThreads = 2

func hashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", fmt.Errorf("Use a password between 12 and 1024 bytes.")
	}
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	hash := argon2.IDKey([]byte(password), salt, passwordIterations, passwordMemory, passwordThreads, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=2$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}
func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(password) > 1024 || len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=2" {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[4])
	if e != nil || len(salt) != 16 {
		return false
	}
	expected, e := base64.RawStdEncoding.DecodeString(parts[5])
	if e != nil || len(expected) != 32 {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, passwordIterations, passwordMemory, passwordThreads, 32)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
