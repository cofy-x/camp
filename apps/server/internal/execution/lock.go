package execution

import (
	"github.com/cofy-x/camp/apps/server/internal/serviceowner"
	"os"
)

var ErrDirectoryInUse = serviceowner.ErrDirectoryInUse

func AcquireLock(dir string) (*os.File, error) { return serviceowner.AcquireDirectory(dir) }
