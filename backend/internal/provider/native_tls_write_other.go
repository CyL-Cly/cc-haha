//go:build !darwin

package provider

import "fmt"

func nativeTLSWrite(*nativeHTTP1Socket, []byte) (int, error) {
	return 0, fmt.Errorf("%w: native TLS record write platform", ErrHTTPExchangeProfile)
}
