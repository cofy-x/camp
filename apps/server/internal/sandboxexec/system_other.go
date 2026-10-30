//go:build !linux && !darwin

package sandboxexec

import "context"

func validateIdentity() error {
	return problem("unsupported_platform", "The sandbox helper requires Linux.")
}

func cancelProcess(string, string) error                   { return validateIdentity() }
func files(context.Context, Request) (Frame, error)        { return Frame{}, validateIdentity() }
func command(context.Context, Request, *frameWriter) error { return validateIdentity() }
