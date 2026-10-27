//go:build !windows

package httpprobe

import (
	"errors"
	"syscall"
)

func isConnectionInterrupted(err error) bool {
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EPIPE)
}
