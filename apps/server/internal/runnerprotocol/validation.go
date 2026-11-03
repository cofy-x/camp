package runnerprotocol

import (
	"encoding/hex"
	"errors"
	"strings"
)

const MaxSources = 32
const MaxSourceIDLength = 48
const MaxSlots = 16

func ValidateInstallation(value *Installation) error {
	if value == nil {
		return nil
	}
	if len(value.Version) < 2 || len(value.Version) > 80 || value.Version[0] != 'v' {
		return errors.New("Invalid Runner installation version")
	}
	for _, c := range value.Version[1:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c == '.' || c == '-') {
			return errors.New("Invalid Runner installation version")
		}
	}
	for _, field := range []struct {
		value string
		bytes int
	}{{value.SourceCommit, 20}, {value.RuntimeSHA256, 32}} {
		decoded, err := hex.DecodeString(field.value)
		if err != nil || len(decoded) != field.bytes || strings.ToLower(field.value) != field.value {
			return errors.New("Invalid Runner installation digest")
		}
	}
	return nil
}

// Source IDs are machine identifiers. The separately advertised display alias
// may contain human-readable text without changing the authorized identity.
func ValidSourceID(id string) bool {
	if len(id) < 1 || len(id) > MaxSourceIDLength || id[0] < 'a' || id[0] > 'z' {
		return false
	}
	for i := 1; i < len(id); i++ {
		b := id[i]
		if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
			return false
		}
	}
	return true
}
