//go:build !linux && !darwin

package documenttext

import (
	"context"
	"io"
)

func initializeReader(context.Context, Config) error { return nil }
func runReader(context.Context, Config, string, io.Reader, io.Writer, io.Writer) error {
	return failure("unavailable", "Isolated document preparation is unsupported on this host. Originals remain downloadable.", true)
}
