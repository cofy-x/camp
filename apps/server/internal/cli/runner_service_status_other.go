//go:build !darwin

package cli

import "context"

// Other platforms keep their existing service command. A Mac launchd report
// must never infer process ownership from a similarly named host service.
func readMacServiceStatus(context.Context, string, string) serviceStatus {
	return serviceStatus{State: "unsupported"}
}
