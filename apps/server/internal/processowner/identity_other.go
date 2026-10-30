//go:build !darwin

package processowner

import "context"

func Observe(int) (Identity, error)          { return Identity{}, ErrUnavailable }
func Members(Identity) ([]Identity, error)   { return nil, ErrUnavailable }
func Signal(Identity, int) error             { return ErrUnavailable }
func terminate(context.Context, Owner) error { return ErrUnavailable }
