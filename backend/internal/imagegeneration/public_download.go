package imagegeneration

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/httphook"
	weburl "github.com/nlnwa/whatwg-url/url"
)

func AssertPublicAddress(address string) error {
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return errors.New("Image download blocked a non-public IP address.")
	}
	if ip.Zone() != "" {
		return fmt.Errorf("%q cannot be parsed as a URL.", "https://["+address+"]/")
	}
	blocked := httphook.IsBlockedAddress(address)
	if ip.Is4() {
		b := ip.As4()
		a, c, d := b[0], b[1], b[2]
		blocked = blocked || a == 127 || a >= 224 || a == 192 && c == 0 && (d == 0 || d == 2) || a == 198 && (c == 18 || c == 19 || c == 51 && d == 100) || a == 203 && c == 0 && d == 113
	} else {
		canonical := ip.String()
		first := ip.As16()[0]
		blocked = blocked || first < 0x20 || first > 0x3f || strings.HasPrefix(canonical, "2002:") || strings.HasPrefix(canonical, "2001:0:") || strings.HasPrefix(canonical, "2001::") || strings.HasPrefix(canonical, "2001:db8:")
	}
	if blocked {
		return errors.New("Image download blocked a non-public IP address.")
	}
	return nil
}

type PublicDownloadOptions struct {
	TLSConfig *tls.Config
	Resolve   func(context.Context, string) ([]net.IPAddr, error)
	Dial      func(context.Context, string, string) (net.Conn, error)
	Timeout   time.Duration
}

// DownloadPublic uses only validated DNS addresses for the actual connection.
// No API credentials, proxy/global agent or redirect is carried to the CDN.
// The native caller schedules this physical operation through its owning async
// delivery and receives immutable bytes; it never runs model/tool state here.
func DownloadPublic(parent context.Context, address string, options PublicDownloadOptions) ([]byte, error) {
	u, err := weburl.Parse(address)
	if err != nil {
		return nil, err
	}
	if u.Scheme() != "https" || u.Username() != "" || u.Password() != "" {
		return nil, errors.New("Image download requires an HTTPS URL without credentials.")
	}
	host := strings.Trim(u.Hostname(), "[]")
	if _, err := netip.ParseAddr(host); err == nil {
		if err := AssertPublicAddress(host); err != nil {
			return nil, err
		}
	}
	if options.Resolve == nil {
		options.Resolve = net.DefaultResolver.LookupIPAddr
	}
	if options.Dial == nil {
		options.Dial = (&net.Dialer{}).DialContext
	}
	if options.Timeout == 0 {
		options.Timeout = time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, options.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: options.TLSConfig, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		hostname, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := options.Resolve(ctx, hostname)
		if err != nil {
			return nil, err
		}
		if len(addresses) == 0 {
			return nil, errors.New("Image download DNS returned no addresses.")
		}
		for _, entry := range addresses {
			if err := AssertPublicAddress(entry.IP.String()); err != nil {
				return nil, err
			}
		}
		return options.Dial(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, "GET", u.Href(false), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "image/*")
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Image download was aborted or timed out.")
		}
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Image download failed with HTTP %d; redirects are not allowed.", response.StatusCode)
	}
	if response.ContentLength > MaxGeneratedBytes {
		return nil, errors.New("The generated image exceeded the 30 MB size limit.")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxGeneratedBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Image download was aborted or timed out.")
		}
		return nil, err
	}
	if len(data) > MaxGeneratedBytes {
		return nil, errors.New("The generated image exceeded the 30 MB size limit.")
	}
	if len(data) == 0 {
		return nil, errors.New("The generated image was empty.")
	}
	return data, nil
}
