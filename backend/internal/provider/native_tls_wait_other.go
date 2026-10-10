//go:build !darwin

package provider

import "fmt"

func newNativeTLSWriteReadiness(*nativeHTTP1Socket) (nativeTLSWriteReadiness, error) {
	return nil, fmt.Errorf("%w: owned native TLS writable platform", ErrHTTPExchangeProfile)
}
func nativeTLSWriteSome(*nativeHTTP1Socket, []byte) (int, bool, error) {
	return 0, false, fmt.Errorf("%w: owned native TLS writable platform", ErrHTTPExchangeProfile)
}
