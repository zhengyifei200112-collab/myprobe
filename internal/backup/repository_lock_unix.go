//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package backup

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockRepository(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}
