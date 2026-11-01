package nativeexecutor

import "fmt"

// Paths belong to the active attempt, including a resumed model session. File
// aliases remain an API compatibility detail, not a host shell mount promise.
func executionGuidance(directory, references string) string {
	text := fmt.Sprintf("\nThe current workspace directory is %q. File discovery defaults to '.'. Use relative paths or this turn's actual workspace paths for file and shell operations, including after session continuation. File-tool compatibility aliases do not create host directories for shell commands.", directory)
	if references != "" {
		text += fmt.Sprintf(" Read-only reference evidence is under %q.", references)
	}
	return text
}
