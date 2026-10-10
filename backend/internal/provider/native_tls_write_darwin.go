//go:build darwin

package provider

import (
	"errors"
	"fmt"
	"syscall"
)

func nativeTLSWrite(socket *nativeHTTP1Socket, out []byte) (int, error) {
	for {
		count, err := syscall.Write(socket.descriptor, out)
		if count < 0 {
			count = 0
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) || err == nil && count != len(out) {
			return count, fmt.Errorf("%w: nonblocking TLS record write backpressure", ErrHTTPExchangeProfile)
		}
		return count, err
	}
}
