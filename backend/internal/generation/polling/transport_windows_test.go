//go:build windows

package polling

import (
	"context"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
)

func TestNativeWindowsTransportErrorsAreRetryable(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "connection reset", err: syscall.WSAECONNRESET},
		{name: "connection aborted", err: syscall.WSAECONNABORTED},
		{name: "broken pipe", err: syscall.ERROR_BROKEN_PIPE},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := &url.Error{
				Op:  "Get",
				URL: "https://provider.example/v1/tasks/task-1",
				Err: &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", test.err)},
			}
			if !IsTransientTransport(err) {
				t.Fatal("native Windows transport error was not classified as transient")
			}
			retry, notFound := Retryable(context.Background(), err)
			if !retry || notFound {
				t.Fatalf("retry = %v, notFound = %v, want transient", retry, notFound)
			}
		})
	}
}
