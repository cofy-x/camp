//go:build !darwin

package nativejob

import (
	"context"
	"errors"
	"io"
)

func Client(context.Context, string, io.Reader, io.Writer, io.Writer) (int, error) {
	return 1, errors.New("Native jobs require macOS")
}
func Worker(context.Context, string) error    { return errors.New("Native jobs require macOS") }
func Reconcile(context.Context, string) error { return errors.New("Native jobs require macOS") }
func Recover(context.Context, string) error   { return errors.New("Native jobs require macOS") }
