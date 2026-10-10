package provider

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
)

// HTTP proxy negotiation is transport work. It consumes only the CONNECT
// response before TLS; the original owner still adopts the provider response.
func nativeProxyAuthority(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("%w: HTTP proxy without credentials or routing path required", ErrHTTPExchangeProfile)
	}
	if u.Port() != "" {
		return u.Host, nil
	}
	return net.JoinHostPort(u.Hostname(), "80"), nil
}

func nativeTargetAuthority(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	port := "80"
	if u.Scheme == "https" {
		port = "443"
	}
	return net.JoinHostPort(u.Hostname(), port)
}

func negotiateNativeProxy(ctx context.Context, connection net.Conn, authority string, limit int) error {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = connection.Close(); close(done) })
	defer func() {
		if !stop() {
			<-done
		}
	}()
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: authority}, Host: authority, Header: make(http.Header)}
	if err := request.Write(connection); err != nil {
		return err
	}
	var header []byte
	unit := []byte{0}
	for len(header) < limit {
		// Do not read ahead: the same TCP resource starts TLS immediately after
		// the proxy header, with no discarded or privately buffered target bytes.
		if _, err := io.ReadFull(connection, unit); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		header = append(header, unit[0])
		if bytes.HasSuffix(header, []byte("\r\n\r\n")) {
			response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(header)), request)
			if err != nil {
				return err
			}
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("%w: HTTP proxy CONNECT status %d", ErrHTTPExchangeProfile, response.StatusCode)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: HTTP proxy CONNECT headers exceed capture bound", ErrHTTPExchangeProfile)
}
