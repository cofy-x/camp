package execution

import "errors"

var ErrDirectoryInUse = errors.New("Another Camp instance is using this task directory")
