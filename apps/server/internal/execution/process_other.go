//go:build !linux && !darwin

package execution

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

func AcquireLock(string) (*os.File, error) { return nil, nil }
func Command(Config, string, *os.File, bool) (*exec.Cmd, error) {
	return nil, fmt.Errorf("Task execution currently requires Linux and Bubblewrap")
}
func ProjectCommand(ProjectConfig, *os.File) (*exec.Cmd, error) {
	return nil, fmt.Errorf("Project environments currently require Linux and Bubblewrap")
}
func Available(context.Context, Config, string, *os.File) error {
	return fmt.Errorf("Task execution currently requires Linux and Bubblewrap")
}
