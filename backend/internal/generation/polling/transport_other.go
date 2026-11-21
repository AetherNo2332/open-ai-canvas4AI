//go:build !windows

package polling

import (
	"errors"
	"syscall"
)

func isTransientSocketError(err error) bool {
	for _, target := range []error{syscall.ECONNRESET, syscall.ECONNABORTED, syscall.EPIPE} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
