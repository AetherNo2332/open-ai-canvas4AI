//go:build windows

package polling

import (
	"errors"
	"syscall"
)

func isTransientSocketError(err error) bool {
	for _, target := range []error{
		syscall.ECONNRESET, syscall.ECONNABORTED, syscall.EPIPE,
		syscall.WSAECONNRESET, syscall.WSAECONNABORTED, syscall.ERROR_BROKEN_PIPE,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
