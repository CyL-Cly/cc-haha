package httphook

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

type bufferedProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedProxyConn) Read(bytes []byte) (int, error) { return c.reader.Read(bytes) }

// Source's pinned Bun HttpsProxyAgent tunnels HTTPS targets. HTTP targets and
// the sandbox proxy use the distinct forward-proxy boundary.
func proxyTunnel(ctx context.Context, proxy *url.URL, network, address string, options NativeOptions, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	if proxy.Scheme != "http" && proxy.Scheme != "https" {
		return nil, &CapabilityError{"original proxy agent scheme " + proxy.Scheme}
	}
	port := proxy.Port()
	if port == "" {
		port = "80"
		if proxy.Scheme == "https" {
			port = "443"
		}
	}
	connection, err := dial(ctx, network, net.JoinHostPort(proxy.Hostname(), port))
	if err != nil {
		return nil, err
	}
	owned := true
	defer func() {
		if owned {
			connection.Close()
		}
	}()
	// Capture the underlying socket once; TLS wrapping below must not race a
	// cancellation callback reading a reassigned interface.
	socket := connection
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { socket.Close(); close(callbackDone) })
	defer func() {
		if !stop() {
			<-callbackDone
		}
	}()
	if deadline, ok := ctx.Deadline(); ok {
		if err = connection.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	if proxy.Scheme == "https" {
		if options.VerifiedTLS == nil {
			return nil, &CapabilityError{"actual original proxy verified TLS configuration"}
		}
		config := options.VerifiedTLS.Clone()
		config.ServerName = proxy.Hostname()
		secured := tls.Client(connection, config)
		if err = secured.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		connection = secured
	}
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: address}, Host: address, Header: http.Header{}}
	request.Header.Set("Proxy-Connection", "close")
	if proxy.User != nil {
		password, _ := proxy.User.Password()
		request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxy.User.Username()+":"+password)))
	}
	if err = request.Write(connection); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		return nil, fmt.Errorf("Proxy connection failed: %d", response.StatusCode)
	}
	// A successful CONNECT response transfers the live socket. Closing its
	// Body would close the tunnel; buffered encrypted bytes stay with this view.
	if err = connection.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	owned = false
	return &bufferedProxyConn{Conn: connection, reader: reader}, nil
}
