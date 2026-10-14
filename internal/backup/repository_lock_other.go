//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package backup

import (
	"errors"
	"os"
)

func lockRepository(*os.File) error { return errors.New("backup repository locking unavailable") }
