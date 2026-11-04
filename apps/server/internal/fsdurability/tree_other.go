//go:build !linux && !darwin

package fsdurability

import (
	"context"
	"errors"
)

func Tree(context.Context, string) error {
	return errors.New("staging tree synchronization is unsupported on this platform")
}
