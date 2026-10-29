package cli

import (
	"errors"
	"fmt"
	"testing"
)

type operatorActionError struct{}

func (operatorActionError) Error() string   { return "Upgrade required" }
func (operatorActionError) Permanent() bool { return true }

func TestExitCodeDistinguishesOperatorActionFromCrash(t *testing.T) {
	for _, input := range []struct {
		err  error
		want int
	}{
		{nil, 0},
		{errors.New("Temporary process failure"), 1},
		{operatorActionError{}, 78},
		{fmt.Errorf("Runner: %w", operatorActionError{}), 78},
	} {
		if got := ExitCode(input.err); got != input.want {
			t.Errorf("ExitCode(%v)=%d, want %d", input.err, got, input.want)
		}
	}
}
