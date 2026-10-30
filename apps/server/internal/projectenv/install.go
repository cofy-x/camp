package projectenv

import "errors"

// CleanupError retains staging and suspends execution until native recovery.
type CleanupError struct{ Err error }

func (e *CleanupError) Error() string   { return "Dependency process cleanup could not be confirmed" }
func (e *CleanupError) Unwrap() error   { return e.Err }
func CleanupUnconfirmed(err error) bool { var unknown *CleanupError; return errors.As(err, &unknown) }
