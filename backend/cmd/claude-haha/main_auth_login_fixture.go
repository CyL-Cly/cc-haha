//go:build native_process_fixture

package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type cliLoginFixtureTransport struct {
	peer     *url.URL
	delegate http.RoundTripper
}

func (t cliLoginFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	target := *request.URL
	target.Scheme = t.peer.Scheme
	target.Host = t.peer.Host
	clone.URL = &target
	clone.Host = request.URL.Host
	return t.delegate.RoundTrip(clone)
}
func cliAuthLoginOptions(native *clibootstrap.NativeBoundary) clibootstrap.AuthLoginOptions {
	value := native.Environment["CC_HAHA_CLI_LOGIN_PEER"]
	if value == "" {
		return clibootstrap.AuthLoginOptions{}
	}
	peer, e := url.Parse(value)
	if e != nil || peer.Scheme != "https" || peer.Hostname() != "127.0.0.1" || peer.Port() == "" {
		panic("unowned CLI login fixture peer")
	}
	config, e := desktopoauth.ResolveConfig(native.Environment)
	if e != nil {
		panic(e)
	}
	tls, e := tlsconfig.NewState(func(path string) ([]byte, error) {
		root := native.Environment["CC_HAHA_NATIVE_FIXTURE_ROOT"]
		canonicalRoot, rootError := filepath.EvalSymlinks(root)
		canonicalPath, pathError := filepath.EvalSymlinks(path)
		if rootError != nil || pathError != nil {
			return nil, errors.New("unowned fixture TLS file")
		}
		rel, e := filepath.Rel(canonicalRoot, canonicalPath)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, errors.New("unowned fixture TLS file")
		}
		return os.ReadFile(path)
	})
	if e != nil {
		panic(e)
	}
	client, e := webfetch.NewHTTPClient(native.Environment, tls)
	if e != nil {
		panic(e)
	}
	client.Transport = cliLoginFixtureTransport{peer, client.Transport}

	return clibootstrap.AuthLoginOptions{Config: &config, HTTP: client, Browser: func(ctx context.Context, address string) error {
		authorize, e := url.Parse(address)
		if e != nil {
			return e
		}
		callback, e := url.Parse(authorize.Query().Get("redirect_uri"))
		if e != nil || callback.Hostname() != "localhost" {
			return errors.New("unowned fixture callback")
		}
		query := callback.Query()
		query.Set("code", "fixture-authorization-code")
		query.Set("state", authorize.Query().Get("state"))
		callback.RawQuery = query.Encode()
		root := native.Environment["CC_HAHA_NATIVE_FIXTURE_ROOT"]
		raw, _ := json.Marshal(map[string]string{"url": address, "callback": callback.String()})
		if e = os.WriteFile(filepath.Join(root, "browser-fixture.json"), raw, 0600); e != nil {
			return e
		}
		if native.Environment["CC_HAHA_CLI_LOGIN_HOLD_BROWSER"] == "1" {
			_, _ = os.Stdout.WriteString("Owned fixture listener ready\n")
			<-ctx.Done()
			return context.Cause(ctx)
		}
		request, e := // Join only this owned fixture browser receipt after the real callback reply.
			// The production opener still receives the cancellable original context.
			http.NewRequestWithContext(context.WithoutCancel(ctx), "GET", callback.String(), nil)
		if e != nil {
			return e
		}
		browserClient := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, e := browserClient.Do(request)
		if e != nil {
			return e
		}
		defer response.Body.Close()
		body, e := io.ReadAll(response.Body)
		if e != nil {
			return e
		}
		raw, _ = json.Marshal(map[string]any{"status": response.StatusCode, "location": response.Header.Get("Location"), "body": string(body)})
		return os.WriteFile(filepath.Join(root, "callback-fixture.json"), raw, 0600)
	}}
}
