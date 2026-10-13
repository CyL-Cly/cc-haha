package httphook

import (
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/andybalholm/brotli"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "HTTP Hook capability is not migrated: " + e.Boundary
}

type NativeOptions struct {
	Environment  map[string]string
	VerifiedTLS  *tls.Config
	SandboxProxy *url.URL
	Resolve      func(context.Context, string) ([]net.IPAddr, error)
	Dial         func(context.Context, string, string) (net.Conn, error)
}
type Result struct {
	OK         bool            `json:"ok"`
	StatusCode *int            `json:"statusCode,omitempty"`
	Body       json.RawMessage `json:"body"`
	Error      json.RawMessage `json:"error,omitempty"`
	Aborted    bool            `json:"aborted,omitempty"`
}

func quote(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func scalar(raw json.RawMessage) (string, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return "", err
	}
	for i := 0; i < len(units); i++ {
		value := units[i]
		if value >= 0xd800 && value <= 0xdbff {
			if i+1 == len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
				return "", &CapabilityError{"unpaired UTF-16 URL"}
			}
			i++
		} else if value >= 0xdc00 && value <= 0xdfff {
			return "", &CapabilityError{"unpaired UTF-16 URL"}
		}
	}
	return string(utf16.Decode(units)), nil
}
func latin1(raw json.RawMessage) (string, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return "", err
	}
	for _, value := range units {
		if value > 255 {
			return "", &CapabilityError{"original Node invalid header character error"}
		}
	}
	// The pinned Source Bun HTTP adapter validates Latin-1 code units but
	// writes their UTF-8 bytes. A raw-byte conversion changes actual headers.
	return string(utf16.Decode(units)), nil
}
func bodyString(bytes []byte) json.RawMessage {
	text := string(protocol.DecodeJavaScriptUTF8(bytes))
	text = strings.TrimPrefix(text, "\ufeff")
	return quote(text)
}

// Execute is a pure transport operation. Its owner binding runs and joins this
// method off the event owner; no Agent state or policy is invoked by workers.
func Execute(ctx context.Context, hook, settings protocol.JSONObject, jsonInput []byte, options NativeOptions) (Result, error) {
	prepared, err := Prepare(hook, settings, options.Environment)
	if err != nil {
		return Result{}, err
	}
	result := Result{Body: quote("")}
	if prepared.Blocked != nil {
		result.Error = prepared.Blocked
		return result, nil
	}
	endpoint, err := scalar(prepared.URL)
	if err != nil {
		return result, err
	}
	target, err := url.Parse(endpoint)
	if err != nil || target.Scheme != "http" && target.Scheme != "https" || target.Hostname() == "" {
		result.Error = quote("Invalid URL")
		return result, nil
	}
	if target.User != nil {
		return result, &CapabilityError{"Axios URL userinfo authorization precedence"}
	}
	timeout := 10 * time.Minute
	var seconds float64
	_ = json.Unmarshal(hook.Get("timeout"), &seconds)
	if seconds != 0 {
		timeout = time.Duration(seconds * float64(time.Second))
	}
	work, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	selection := provider.ResolveNativeProxySelection(endpoint, options.Environment)
	var proxy *url.URL
	sandboxProxy := options.SandboxProxy != nil
	if options.SandboxProxy != nil {
		clone := *options.SandboxProxy
		proxy = &clone
	} else if selection.ProxyURL != "" && !selection.Bypass {
		proxy, err = url.Parse(selection.ProxyURL)
		if err != nil {
			return result, err
		}
	}
	dial := options.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	resolve := options.Resolve
	if resolve == nil {
		resolve = net.DefaultResolver.LookupIPAddr
	}
	guardedDial := dial
	// Pinned Bun uses a forward proxy for HTTP targets even when the original
	// HttpsProxyAgent is installed, and CONNECT for HTTPS targets.
	forwardProxy := proxy != nil && (sandboxProxy || target.Scheme == "http")
	if proxy != nil && !forwardProxy {
		guardedDial = func(ctx context.Context, network, address string) (net.Conn, error) {
			return proxyTunnel(ctx, proxy, network, address, options, dial)
		}
	}
	if proxy == nil {
		guardedDial = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			var addresses []net.IPAddr
			if AddressFamily(host) != 0 {
				if IsBlockedAddress(host) {
					return nil, errors.New(BlockedAddressMessage(host, host))
				}
				return dial(ctx, network, net.JoinHostPort(host, port))
			} else {
				addresses, err = resolve(ctx, host)
				if err != nil {
					return nil, err
				}
			}
			for _, address := range addresses {
				if IsBlockedAddress(address.String()) {
					return nil, errors.New(BlockedAddressMessage(host, address.String()))
				}
			}
			if len(addresses) == 0 {
				return nil, fmt.Errorf("ENOTFOUND %s", host)
			}
			var last error
			for _, address := range addresses {
				connection, err := dial(ctx, network, net.JoinHostPort(address.String(), port))
				if err == nil {
					return connection, nil
				}
				last = err
				if ctx.Err() != nil {
					break
				}
			}
			return nil, last
		}
	}
	transport := &http.Transport{DialContext: guardedDial, DisableCompression: true, ForceAttemptHTTP2: false, TLSClientConfig: options.VerifiedTLS}
	if forwardProxy {
		transport.Proxy = http.ProxyURL(proxy)
	}
	defer transport.CloseIdleConnections()
	if target.Scheme == "https" && options.VerifiedTLS == nil {
		return result, &CapabilityError{"actual original verified TLS configuration"}
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(work, http.MethodPost, endpoint, strings.NewReader(string(jsonInput)))
	if err != nil {
		result.Error = quote(err.Error())
		return result, nil
	}
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("User-Agent", "axios/1.15.2")
	request.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
	for _, header := range prepared.Headers {
		name, err := scalar(header.Name)
		if err != nil {
			return result, err
		}
		value, err := latin1(header.Value)
		if err != nil {
			return result, err
		}
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		if work.Err() != nil {
			result.Aborted = true
		} else {
			var urlError *url.Error
			if errors.As(err, &urlError) {
				err = urlError.Err
			}
			result.Error = quote(err.Error())
		}
		return result, nil
	}
	defer response.Body.Close()
	reader := io.Reader(response.Body)
	var decoder io.ReadCloser
	switch strings.ToLower(response.Header.Get("Content-Encoding")) {
	case "gzip", "x-gzip", "compress", "x-compress":
		decoder, err = gzip.NewReader(reader)
	case "deflate":
		buffered, readErr := io.ReadAll(reader)
		if readErr != nil {
			err = readErr
			break
		}
		decoder, err = zlib.NewReader(strings.NewReader(string(buffered)))
		if err != nil {
			decoder = flate.NewReader(strings.NewReader(string(buffered)))
			err = nil
		}
	case "br":
		reader = brotli.NewReader(reader)
	}
	if err != nil {
		result.Error = quote(err.Error())
		return result, nil
	}
	if decoder != nil {
		defer decoder.Close()
		reader = decoder
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		if work.Err() != nil {
			result.Aborted = true
		} else {
			result.Error = quote(err.Error())
		}
		return result, nil
	}
	result.OK = response.StatusCode >= 200 && response.StatusCode < 300
	result.StatusCode = &response.StatusCode
	result.Body = bodyString(data)
	return result, nil
}
