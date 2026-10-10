package sandboxruntime

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	whatwg "github.com/nlnwa/whatwg-url/url"
)

type parentProxyURL struct {
	*url.URL
	href, authentication string
}

func (p *parentProxyURL) String() string { return p.href }

type parentProxy struct {
	http, https *parentProxyURL
	all         bool
	suffixes    []string
	networks    []*net.IPNet
}

func parseParentProxy(environment map[string]string) *parentProxy {
	get := func(upper, lower string) string {
		if value, ok := environment[upper]; ok {
			return value
		}
		return environment[lower]
	}
	httpRaw := get("HTTP_PROXY", "http_proxy")
	httpsRaw := get("HTTPS_PROXY", "https_proxy")
	if _, upper := environment["HTTPS_PROXY"]; !upper {
		if _, lower := environment["https_proxy"]; !lower {
			httpsRaw = httpRaw
		}
	}
	parse := func(value string) *parentProxyURL {
		if value == "" {
			return nil
		}
		hasScheme := false
		if colon := strings.Index(value, "://"); colon > 0 {
			hasScheme = true
			for i, r := range value[:colon] {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.')) {
					hasScheme = false
					break
				}
			}
		}
		if !hasScheme {
			value = "http://" + value
		}
		parsed, e := whatwg.Parse(value)
		if e != nil || parsed.Hostname() == "" || parsed.Scheme() != "http" && parsed.Scheme() != "https" {
			return nil
		}
		username, password := parsed.Username(), parsed.Password()
		authentication := ""
		if username != "" || password != "" {
			u, ue := url.PathUnescape(username)
			p, pe := url.PathUnescape(password)
			if ue == nil && pe == nil && utf8.ValidString(u) && utf8.ValidString(p) {
				username, password = u, p
			}
			authentication = "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		}
		return &parentProxyURL{URL: &url.URL{Scheme: parsed.Scheme(), Host: parsed.Host()}, href: parsed.Href(false), authentication: authentication}
	}
	result := &parentProxy{http: parse(httpRaw), https: parse(httpsRaw)}
	if result.http == nil && result.https == nil {
		return nil
	}
	for _, entry := range strings.Split(get("NO_PROXY", "no_proxy"), ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry == "*" {
			result.all = true
			continue
		}
		if strings.Contains(entry, "/") {
			if _, network, e := net.ParseCIDR(entry); e == nil {
				result.networks = append(result.networks, network)
			}
			continue
		}
		value := strings.ToLower(entry)
		if strings.HasPrefix(value, "[") {
			if end := strings.IndexByte(value, ']'); end >= 0 {
				after := value[end+1:]
				if after == "" || strings.HasPrefix(after, ":") && decimalDigits(after[1:]) {
					value = value[1:end]
				}
			}
		}
		if strings.HasPrefix(value, "*.") {
			value = value[1:]
		}
		if net.ParseIP(value) == nil {
			if colon := strings.LastIndexByte(value, ':'); colon >= 0 {
				if decimalDigits(value[colon+1:]) {
					value = value[:colon]
				}
			}
		}
		if ip := net.ParseIP(value); ip != nil {
			bits := 128
			if ip.To4() != nil {
				ip = ip.To4()
				bits = 32
			}
			result.networks = append(result.networks, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		} else {
			result.suffixes = append(result.suffixes, value)
		}
	}
	return result
}
func decimalDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
func (p *parentProxy) bypass(host string) bool {
	host = stripBrackets(strings.TrimSuffix(strings.ToLower(host), "."))
	ip := net.ParseIP(host)
	if host == "localhost" || ip != nil && ip.IsLoopback() {
		return true
	}
	if p.all {
		return true
	}
	if ip != nil {
		for _, network := range p.networks {
			if network.Contains(ip) {
				return true
			}
		}
	}
	for _, suffix := range p.suffixes {
		if strings.HasPrefix(suffix, ".") {
			if host == suffix[1:] || strings.HasSuffix(host, suffix) {
				return true
			}
		} else if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}
func (p *parentProxy) selectURL(host string, https bool) *parentProxyURL {
	if p == nil || p.bypass(host) {
		return nil
	}
	if https {
		if p.https != nil {
			return p.https
		}
		return p.http
	}
	return p.http
}
func proxyAddress(proxy *parentProxyURL) string {
	port := proxy.Port()
	if port == "" {
		port = "80"
		if proxy.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(proxy.Hostname(), port)
}
func proxyAuth(proxy *parentProxyURL) string { return proxy.authentication }

type bufferedTunnel struct {
	net.Conn
	reader io.Reader
}

func (c *bufferedTunnel) Read(b []byte) (int, error) { return c.reader.Read(b) }
func openParentTunnel(ctx context.Context, proxy *parentProxyURL, host string, port int, verified *tls.Config, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	if !ValidHost(stripBrackets(host)) {
		return nil, fmt.Errorf("Invalid destination host for CONNECT: %q", host)
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("Invalid destination port: %d", port)
	}
	active, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	connection, e := dial(active, "tcp", proxyAddress(proxy))
	if e != nil {
		return nil, e
	}
	transferred := false
	defer func() {
		if !transferred {
			connection.Close()
		}
	}()
	socket := connection
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(active, func() { socket.Close(); close(callbackDone) })
	defer func() {
		if !stop() {
			<-callbackDone
		}
	}()
	if deadline, ok := active.Deadline(); ok {
		connection.SetDeadline(deadline)
	}
	if proxy.Scheme == "https" {
		if verified == nil {
			return nil, errors.New("sandbox parent proxy requires original verified TLS")
		}
		config := verified.Clone()
		config.ServerName = proxy.Hostname()
		secured := tls.Client(connection, config)
		if e = secured.HandshakeContext(active); e != nil {
			return nil, e
		}
		connection = secured
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	header := "CONNECT " + address + " HTTP/1.1\r\nHost: " + address + "\r\n"
	if auth := proxyAuth(proxy); auth != "" {
		header += "Proxy-Authorization: " + auth + "\r\n"
	}
	if _, e = fmt.Fprint(connection, header+"\r\n"); e != nil {
		return nil, e
	}
	// Source evaluates only the status line; it does not parse or interpret
	// response headers. Its 16 KiB bound applies to an unterminated header.
	var response []byte
	block := make([]byte, 32*1024)
	end := -1
	for end < 0 {
		count, failure := connection.Read(block)
		if count > 0 {
			response = append(response, block[:count]...)
			end = bytes.Index(response, []byte("\r\n\r\n"))
			if end < 0 && len(response) > 16*1024 {
				return nil, errors.New("CONNECT response header too large")
			}
		}
		if end >= 0 {
			break
		}
		if failure != nil {
			return nil, errors.New("Proxy closed during CONNECT handshake")
		}
	}
	lineEnd := bytes.Index(response, []byte("\r\n"))
	status := response[:lineEnd]
	// Node's buffer.toString('latin1') maps each byte directly to a code point.
	statusRunes := make([]rune, len(status))
	for i, value := range status {
		statusRunes[i] = rune(value)
	}
	line := string(statusRunes)
	if !parentConnectStatus.MatchString(line) {
		return nil, fmt.Errorf("Proxy refused CONNECT: %s", protocol.TrimJavaScriptString(line))
	}
	reader := io.MultiReader(bytes.NewReader(response[end+4:]), connection)
	connection.SetDeadline(time.Time{})
	if active.Err() != nil {
		return nil, active.Err()
	}
	transferred = true
	return &bufferedTunnel{Conn: connection, reader: reader}, nil
}

var parentConnectStatus = regexp.MustCompile(`^HTTP/1\.[01] 2[0-9]{2}(?:[\x09-\x0d \xa0]|$)`)
