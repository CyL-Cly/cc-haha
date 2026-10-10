//go:build !darwin

package provider

import (
	"fmt"
	"net"
)

// Runtime capability only: other targets still compile, but no unproved native
// descriptor/poller implementation is silently substituted for Darwin Select.
type nativeHTTP1Socket struct{}

func newNativeHTTP1Socket(*net.TCPConn) (*nativeHTTP1Socket, error) {
	return nil, fmt.Errorf("%w: native readiness platform is not Darwin", ErrHTTPExchangeProfile)
}
func (*nativeHTTP1Socket) read([]byte) (int, error) {
	return 0, fmt.Errorf("%w: native readiness platform is not Darwin", ErrHTTPExchangeProfile)
}
func (*nativeHTTP1Socket) wake() {}
func (*nativeHTTP1Socket) wait() error {
	return fmt.Errorf("%w: native readiness platform is not Darwin", ErrHTTPExchangeProfile)
}
func (*nativeHTTP1Socket) shutdown() {}
func (*nativeHTTP1Socket) close()    {}
