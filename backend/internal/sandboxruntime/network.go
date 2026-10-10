package sandboxruntime

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	whatwg "github.com/nlnwa/whatwg-url/url"
)

type NetworkOptions struct {
	Environment map[string]string
	VerifiedTLS *tls.Config
	TLSConfig   func() (*tls.Config, error)
	Dial        func(context.Context, string, string) (net.Conn, error)
	Ask         func(context.Context, string, int) (bool, error)
}
type Network struct {
	mu                  sync.Mutex
	config              NetworkConfig
	options             NetworkOptions
	parent              *parentProxy
	ctx                 context.Context
	cancel              context.CancelFunc
	httpPort, socksPort float64
	httpServer          *http.Server
	listeners           []net.Listener
	connections         map[net.Conn]struct{}
	wg                  sync.WaitGroup
	closing             bool
	closeOnce           sync.Once
	closeDone           chan struct{}
}

// NewNetwork initializes the same HTTP+SOCKS listeners as the installed
// manager. External configured ports are borrowed, not listeners we may close.
func NewNetwork(parent context.Context, config NetworkConfig, options NetworkOptions) (*Network, error) {
	ctx, cancel := context.WithCancel(parent)
	n := &Network{ctx: ctx, cancel: cancel, options: options, config: cloneNetworkConfig(config), connections: map[net.Conn]struct{}{}, closeDone: make(chan struct{})}
	env := map[string]string{}
	for key, value := range options.Environment {
		env[key] = value
	}
	n.options.Environment = env
	n.parent = parseParentProxy(env)
	if n.options.Dial == nil {
		n.options.Dial = (&net.Dialer{}).DialContext
	}
	failure := func(e error) (*Network, error) { n.Close(context.Background()); return nil, e }
	if config.HTTPProxyPort != nil {
		n.httpPort = *config.HTTPProxyPort
	} else {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			return failure(e)
		}
		n.httpPort = float64(listener.Addr().(*net.TCPAddr).Port)
		n.listeners = append(n.listeners, listener)
		n.httpServer = &http.Server{Handler: http.HandlerFunc(n.handleHTTP), BaseContext: func(net.Listener) context.Context { return n.ctx }, ConnState: func(connection net.Conn, state http.ConnState) {
			if state == http.StateNew {
				n.track(connection)
			} else if state == http.StateClosed {
				n.untrack(connection)
			}
		}}
		n.wg.Add(1)
		go func() { defer n.wg.Done(); _ = n.httpServer.Serve(listener) }()
	}
	if config.SOCKSProxyPort != nil {
		n.socksPort = *config.SOCKSProxyPort
	} else {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			return failure(e)
		}
		n.socksPort = float64(listener.Addr().(*net.TCPAddr).Port)
		n.listeners = append(n.listeners, listener)
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			for {
				connection, e := listener.Accept()
				if e != nil {
					return
				}
				if !n.track(connection) {
					continue
				}
				n.wg.Add(1)
				go func() {
					defer n.wg.Done()
					defer n.untrack(connection)
					defer connection.Close()
					n.handleSOCKS(connection)
				}()
			}
		}()
	}
	return n, nil
}
func cloneNetworkConfig(config NetworkConfig) NetworkConfig {
	config.AllowedDomains = cloneSlice(config.AllowedDomains)
	config.DeniedDomains = cloneSlice(config.DeniedDomains)
	if config.AllowUnixSockets != nil {
		copy := cloneSlice(*config.AllowUnixSockets)
		config.AllowUnixSockets = &copy
	}
	config.AllowAllUnixSockets = clonePointer(config.AllowAllUnixSockets)
	config.AllowLocalBinding = clonePointer(config.AllowLocalBinding)
	config.HTTPProxyPort = clonePointer(config.HTTPProxyPort)
	config.SOCKSProxyPort = clonePointer(config.SOCKSProxyPort)
	return config
}
func (n *Network) Ports() (float64, float64) { return n.httpPort, n.socksPort }
func (n *Network) Update(config NetworkConfig) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.config = cloneNetworkConfig(config)
}
func (n *Network) track(connection net.Conn) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closing {
		connection.Close()
		return false
	}
	n.connections[connection] = struct{}{}
	return true
}
func (n *Network) untrack(connection net.Conn) {
	n.mu.Lock()
	delete(n.connections, connection)
	n.mu.Unlock()
}
func (n *Network) allowed(ctx context.Context, host string, port int) bool {
	n.mu.Lock()
	config := cloneNetworkConfig(n.config)
	n.mu.Unlock()
	// Source's synthetic host permission is a process-lifetime request, not
	// tied to the client socket. Check clientGone only after it settles.
	return FilterNetworkRequest(n.ctx, &config, host, port, n.options.Ask)
}
func (n *Network) Close(ctx context.Context) error {
	n.closeOnce.Do(func() {
		n.mu.Lock()
		n.closing = true
		n.cancel()
		for _, listener := range n.listeners {
			listener.Close()
		}
		for connection := range n.connections {
			connection.Close()
		}
		n.mu.Unlock()
		if n.httpServer != nil {
			_ = n.httpServer.Close()
		}
		go func() { n.wg.Wait(); close(n.closeDone) }()
	})
	select {
	case <-n.closeDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (n *Network) dial(ctx context.Context, host string, port int, https bool) (net.Conn, error) {
	if proxy := n.parent.selectURL(host, https); proxy != nil {
		var verified *tls.Config
		var e error
		if proxy.Scheme == "https" {
			verified, e = n.verifiedTLS()
			if e != nil {
				return nil, e
			}
		}
		connection, e := openParentTunnel(ctx, proxy, host, port, verified, n.options.Dial)
		if e != nil {
			return nil, e
		}
		if !n.track(connection) {
			return nil, net.ErrClosed
		}
		return connection, nil
	}
	active, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	connection, e := n.options.Dial(active, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if e != nil {
		return nil, e
	}
	if !n.track(connection) {
		return nil, net.ErrClosed
	}
	return connection, nil
}

var hopHeaders = map[string]bool{"connection": true, "keep-alive": true, "proxy-authenticate": true, "proxy-authorization": true, "proxy-connection": true, "te": true, "trailer": true, "transfer-encoding": true, "upgrade": true}

func stripHopHeaders(headers http.Header) http.Header {
	extra := map[string]bool{}
	for _, value := range headers.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			extra[strings.ToLower(strings.TrimSpace(token))] = true
		}
	}
	out := http.Header{}
	for name, values := range headers {
		key := strings.ToLower(name)
		if !hopHeaders[key] && !extra[key] {
			out[name] = append([]string{}, values...)
		}
	}
	return out
}
func (n *Network) handleHTTP(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	if n.closing {
		n.mu.Unlock()
		return
	}
	n.wg.Add(1)
	n.mu.Unlock()
	defer n.wg.Done()
	if r.Method == http.MethodConnect {
		n.handleCONNECT(w, r)
		return
	}
	target, e := whatwg.Parse(r.RequestURI)
	if e != nil {
		w.WriteHeader(500)
		io.WriteString(w, "Internal Server Error")
		return
	}
	host := stripBrackets(target.Hostname())
	port := 80
	if target.Protocol() == "https:" {
		port = 443
	}
	if target.Port() != "" {
		port, e = strconv.Atoi(target.Port())
		if e != nil {
			w.WriteHeader(500)
			io.WriteString(w, "Internal Server Error")
			return
		}
	}
	if !n.allowed(r.Context(), host, port) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Proxy-Error", "blocked-by-allowlist")
		w.WriteHeader(403)
		io.WriteString(w, "Connection blocked by network allowlist")
		return
	}
	if r.Context().Err() != nil {
		return
	}
	absolute := target.Protocol() + "//" + target.Host() + target.Pathname() + target.Search()
	protocol := "http:"
	if target.Protocol() == "https:" {
		protocol = "https:"
	}
	request, e := http.NewRequestWithContext(r.Context(), r.Method, protocol+"//"+target.Host()+target.Pathname()+target.Search(), r.Body)
	if e != nil {
		w.WriteHeader(500)
		io.WriteString(w, "Internal Server Error")
		return
	}
	request.Header = stripHopHeaders(r.Header)
	request.Host = target.Host()
	request.ContentLength = r.ContentLength
	transport := &http.Transport{DisableCompression: true, ForceAttemptHTTP2: false, DialContext: n.options.Dial}
	if proxy := n.parent.selectURL(host, target.Protocol() == "https:"); proxy != nil {
		// Source forwards an absolute URI to its parent for HTTP requests,
		// including absolute https targets; CONNECT is its distinct handler.
		request.URL = &url.URL{Scheme: proxy.Scheme, Host: proxy.Host, Opaque: absolute}
		if auth := proxyAuth(proxy); auth != "" {
			request.Header.Set("Proxy-Authorization", auth)
		}
	}
	if request.URL.Scheme == "https" {
		verified, failure := n.verifiedTLS()
		if failure != nil {
			w.WriteHeader(502)
			io.WriteString(w, "Bad Gateway")
			return
		}
		transport.TLSClientConfig = verified
	}
	defer transport.CloseIdleConnections()
	response, e := transport.RoundTrip(request)
	if e != nil {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(502)
		io.WriteString(w, "Bad Gateway")
		return
	}
	defer response.Body.Close()
	for key, values := range stripHopHeaders(response.Header) {
		w.Header()[key] = values
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}
func parseConnectTarget(value string) (string, int, bool) {
	host, portString, e := net.SplitHostPort(value)
	if e != nil || portString == "" {
		return "", 0, false
	}
	for _, char := range portString {
		if char < '0' || char > '9' {
			return "", 0, false
		}
	}
	port, e := strconv.Atoi(portString)
	return host, port, e == nil && port >= 1 && port <= 65535
}
func (n *Network) handleCONNECT(w http.ResponseWriter, r *http.Request) {
	host, port, valid := parseConnectTarget(r.RequestURI)
	if !valid {
		w.WriteHeader(400)
		return
	}
	if !n.allowed(r.Context(), host, port) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Proxy-Error", "blocked-by-allowlist")
		w.WriteHeader(403)
		io.WriteString(w, "Connection blocked by network allowlist")
		return
	}
	upstream, e := n.dial(r.Context(), host, port, true)
	if e != nil {
		w.WriteHeader(502)
		return
	}
	defer upstream.Close()
	defer n.untrack(upstream)
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		w.WriteHeader(500)
		return
	}
	client, buffer, e := hijacker.Hijack()
	if e != nil {
		return
	}
	defer client.Close()
	defer n.untrack(client)
	if !n.track(client) {
		return
	}
	if _, e = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); e != nil {
		return
	}
	n.pipe(client, upstream, buffer)
}
func (n *Network) pipe(client, upstream net.Conn, clientReader io.Reader) {
	done := make(chan struct{})
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		_, _ = io.Copy(upstream, clientReader)
		client.Close()
		upstream.Close()
		close(done)
	}()
	_, _ = io.Copy(client, upstream)
	client.Close()
	upstream.Close()
	<-done
}
func (n *Network) handleSOCKS(client net.Conn) {
	read := func(size int) ([]byte, error) {
		value := make([]byte, size)
		_, e := io.ReadFull(client, value)
		return value, e
	}
	version, e := read(1)
	if e != nil || version[0] != 5 {
		return
	}
	count, e := read(1)
	if e != nil || count[0] == 0 || count[0] > 128 {
		return
	}
	methods, e := read(int(count[0]))
	if e != nil {
		return
	}
	noAuth := false
	for _, method := range methods {
		if method == 0 {
			noAuth = true
		}
	}
	if !noAuth {
		client.Write([]byte{5, 255})
		return
	}
	if _, e = client.Write([]byte{5, 0}); e != nil {
		return
	}
	header, e := read(4)
	if e != nil {
		return
	}
	if header[1] == 0 || header[1] > 3 {
		return
	}
	host := ""
	switch header[3] {
	case 1:
		value, e := read(4)
		if e != nil {
			return
		}
		host = net.IP(value).String()
	case 3:
		count, e := read(1)
		if e != nil {
			return
		}
		value, e := read(int(count[0]))
		if e != nil {
			return
		}
		host = string(value)
	case 4:
		value, e := read(16)
		if e != nil {
			return
		}
		parts := []string{}
		for i := 0; i < 16; i += 2 {
			parts = append(parts, fmt.Sprintf("%02x%02x", value[i], value[i+1]))
		}
		host = strings.Join(parts, ":")
	default:
		return
	}
	encoded, e := read(2)
	if e != nil {
		return
	}
	port := int(encoded[0])<<8 | int(encoded[1])
	if header[1] != 1 {
		client.Write([]byte{5, 7})
		return
	}
	status := func(value byte) error { _, e := client.Write([]byte{5, value, 0, 1, 0, 0, 0, 0, 0, 0}); return e }
	if !ValidHost(host) || !n.allowed(n.ctx, host, port) {
		status(2)
		return
	}
	upstream, e := n.dial(n.ctx, host, port, true)
	if e != nil {
		status(4)
		return
	}
	defer upstream.Close()
	defer n.untrack(upstream)
	if e = status(0); e != nil {
		return
	}
	n.pipe(client, upstream, client)
}

func (n *Network) verifiedTLS() (*tls.Config, error) {
	if n.options.TLSConfig != nil {
		return n.options.TLSConfig()
	}
	if n.options.VerifiedTLS != nil {
		return n.options.VerifiedTLS.Clone(), nil
	}
	return nil, fmt.Errorf("sandbox HTTPS requires original verified TLS")
}
