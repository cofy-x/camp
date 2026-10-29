package cli

import "errors"

// ExitNeedsAttention tells a service manager that an operator must repair the
// installation or authorization before restarting this process can help.
const ExitNeedsAttention = 78

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var permanent interface{ Permanent() bool }
	if errors.As(err, &permanent) && permanent.Permanent() {
		return ExitNeedsAttention
	}
	return 1
}
