//go:build darwin

package sandboxexec

import "context"

func validateIdentity() error { return nil }
func cancelProcess(string, string) error {
	return problem("unsupported_operation", "Native cancellation requires an owned job receipt.")
}
func command(context.Context, Request, *frameWriter) error {
	return problem("unsupported_operation", "Native commands require an owned Seatbelt job.")
}
