package httpprobe

import (
	"errors"
	"syscall"
)

func isConnectionInterrupted(err error) bool {
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.WSAECONNRESET) || errors.Is(err, syscall.WSAECONNABORTED) ||
		errors.Is(err, syscall.ERROR_NETNAME_DELETED) || errors.Is(err, syscall.ERROR_BROKEN_PIPE)
}
