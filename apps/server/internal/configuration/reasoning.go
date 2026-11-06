package configuration

// ValidReasoningEffort accepts the explicit public DSH Messages adapter controls.
// Empty retains the provider default and preserves historical configurations.
func ValidReasoningEffort(value string) bool {
	switch value {
	case "", "off", "low", "high", "max":
		return true
	default:
		return false
	}
}
