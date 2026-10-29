package runnerprotocol

const MaxSources = 32
const MaxSourceIDLength = 48
const MaxSlots = 16

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
