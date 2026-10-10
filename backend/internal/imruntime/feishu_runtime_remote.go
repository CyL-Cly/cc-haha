package imruntime

import (
	"context"
	"crypto/tls"
	"fmt"
	whatwg "github.com/nlnwa/whatwg-url/url"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

type FeishuRemoteAddress struct {
	Address string `json:"address"`
	Family  int    `json:"family"`
}
type FeishuRemoteOptions struct {
	Resolve func(context.Context, string) ([]FeishuRemoteAddress, error)
	Request func(context.Context, *whatwg.Url, FeishuRemoteAddress, time.Duration) (*http.Response, func(), error)
	Timeout time.Duration
	Now     func() time.Time
}

func FeishuPublicRemoteAddress(value string) bool {
	value = strings.TrimPrefix(strings.TrimSuffix(value, "]"), "[")
	addr, e := netip.ParseAddr(value)
	if e != nil {
		return false
	}
	addr = addr.Unmap()
	if addr.Is4() {
		o := addr.As4()
		a, b, c := o[0], o[1], o[2]
		if a == 0 || a == 10 || a == 127 || a == 100 && b >= 64 && b <= 127 || a == 169 && b == 254 || a == 172 && b >= 16 && b <= 31 || a == 192 && b == 0 && (c == 0 || c == 2) || a == 192 && b == 88 && c == 99 || a == 192 && b == 168 || a == 198 && (b == 18 || b == 19) || a == 198 && b == 51 && c == 100 || a == 203 && b == 0 && c == 113 || a >= 224 {
			return false
		}
		return true
	}
	o := addr.As16()
	first, second := uint16(o[0])<<8|uint16(o[1]), uint16(o[2])<<8|uint16(o[3])
	if first < 0x2000 || first > 0x3fff || first == 0x2001 && (second == 0xdb8 || second <= 0x1ff) || first == 0x2002 || first == 0x3fff && second <= 0xfff {
		return false
	}
	return true
}
func feishuResolveRemote(ctx context.Context, host string) ([]FeishuRemoteAddress, error) {
	answers, e := net.DefaultResolver.LookupIPAddr(ctx, host)
	if e != nil {
		return nil, e
	}
	out := []FeishuRemoteAddress{}
	for _, answer := range answers {
		family := 6
		if answer.IP.To4() != nil {
			family = 4
		}
		out = append(out, FeishuRemoteAddress{answer.IP.String(), family})
	}
	return out, nil
}
func feishuRequestRemote(ctx context.Context, u *whatwg.Url, address FeishuRemoteAddress, remaining time.Duration) (*http.Response, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, remaining)
	host := strings.Trim(u.Hostname(), "[]")
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Protocol() == "https:" {
			port = "443"
		}
	}
	tr := &http.Transport{Proxy: nil, DisableCompression: true, MaxResponseHeaderBytes: 65536, TLSClientConfig: &tls.Config{ServerName: host}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: remaining}).DialContext(ctx, network, net.JoinHostPort(address.Address, port))
	}}
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, "GET", u.Href(false), nil)
	if e != nil {
		cancel()
		return nil, nil, e
	}
	req.Header.Set("Accept", "image/jpeg, image/png, image/gif, image/webp")
	req.Header.Set("User-Agent", "claude-code-haha-im-adapter")
	response, e := client.Do(req)
	if e != nil {
		cancel()
		tr.CloseIdleConnections()
		return nil, nil, e
	}
	destroy := func() { cancel(); _ = response.Body.Close(); tr.CloseIdleConnections() }
	return response, destroy, nil
}
func FeishuLoadRemoteImage(ctx context.Context, rawURL string, o FeishuRemoteOptions) SafeImage {
	failure := func(s string) SafeImage { return SafeImage{Reason: s} }
	parse := func(value string) (*whatwg.Url, string) {
		u, e := whatwg.Parse(value)
		if e != nil {
			return nil, "remote image URL is invalid"
		}
		if u.Protocol() != "http:" && u.Protocol() != "https:" {
			return nil, "remote image URL must use http or https"
		}
		if u.Username() != "" || u.Password() != "" {
			return nil, "remote image URL must not contain credentials"
		}
		return u, ""
	}
	current, reason := parse(rawURL)
	if current == nil {
		return failure(reason)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 10 * time.Second
	}
	if o.Resolve == nil {
		o.Resolve = feishuResolveRemote
	}
	if o.Request == nil {
		o.Request = feishuRequestRemote
	}
	deadline := o.Now().Add(o.Timeout)
	for redirects := 0; redirects <= 5; redirects++ {
		remaining := deadline.Sub(o.Now())
		if remaining <= 0 {
			return failure("remote image request failed: remote image request timed out")
		}
		ctx, cancel := context.WithTimeout(ctx, remaining)
		host := strings.Trim(current.Hostname(), "[]")
		addresses := []FeishuRemoteAddress{}
		if addr, e := netip.ParseAddr(host); e == nil {
			family := 6
			if addr.Is4() {
				family = 4
			}
			addresses = append(addresses, FeishuRemoteAddress{host, family})
		} else {
			var e error
			addresses, e = o.Resolve(ctx, host)
			if e != nil {
				cancel()
				return failure("remote image request failed: " + e.Error())
			}
		}
		allPublic := len(addresses) > 0
		for _, address := range addresses {
			allPublic = allPublic && FeishuPublicRemoteAddress(address.Address)
		}
		if !allPublic {
			cancel()
			return failure("remote image host resolved to a non-public address")
		}
		remaining = deadline.Sub(o.Now())
		if remaining <= 0 {
			cancel()
			return failure("remote image request failed: remote image request timed out")
		}
		hop, destroy, e := o.Request(ctx, current, addresses[0], remaining)
		if e != nil {
			cancel()
			return failure("remote image request failed: " + e.Error())
		}
		closeHop := func() { destroy(); cancel() }
		if hop.StatusCode == 301 || hop.StatusCode == 302 || hop.StatusCode == 303 || hop.StatusCode == 307 || hop.StatusCode == 308 {
			location := hop.Header.Get("location")
			closeHop()
			if location == "" {
				return failure("remote image redirect is missing a location")
			}
			if redirects == 5 {
				return failure("remote image exceeded the redirect limit")
			}
			next, e := whatwg.ParseRef(current.Href(false), location)
			if e != nil {
				return failure("remote image request failed: Invalid URL")
			}
			current, reason = parse(next.Href(false))
			if current == nil {
				return failure(reason)
			}
			continue
		}
		if hop.StatusCode != 200 {
			closeHop()
			return failure(fmt.Sprintf("remote image request returned status %d", hop.StatusCode))
		}
		mime := strings.ToLower(strings.TrimSpace(strings.SplitN(hop.Header.Get("content-type"), ";", 2)[0]))
		if !CheckAttachmentLimit("image", 0, mime).OK || mime == "" {
			closeHop()
			if mime == "" {
				mime = "missing"
			}
			return failure("remote image has unsupported MIME type: " + mime)
		}
		if encoding := hop.Header.Get("content-encoding"); encoding != "" && strings.ToLower(encoding) != "identity" {
			closeHop()
			return failure("remote image uses an unsupported content encoding")
		}
		if length := hop.Header.Get("content-length"); length != "" {
			n := qqNumber(raw(length))
			if !math.IsNaN(n) && !math.IsInf(n, 0) && n > ImageMaxBytes {
				closeHop()
				return failure("remote image exceeds the 10 MB limit")
			}
		}
		bytes, e := io.ReadAll(io.LimitReader(hop.Body, ImageMaxBytes+1))
		closeHop()
		if e != nil {
			return failure("remote image request failed: " + e.Error())
		}
		if len(bytes) > ImageMaxBytes {
			return failure("remote image exceeds the 10 MB limit")
		}
		return SafeImage{OK: true, Buffer: bytes, MIME: mime}
	}
	return failure("remote image request failed")
}
