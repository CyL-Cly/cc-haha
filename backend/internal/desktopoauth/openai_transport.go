package desktopoauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/networksettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) exchangeOpenAICode(ctx context.Context, code, redirectURI, verifier string) (protocol.JSONObject, error) {
	return s.openAITokenRequest(ctx, "exchange", [][2]string{{"grant_type", "authorization_code"}, {"code", code}, {"redirect_uri", redirectURI}, {"client_id", OpenAICodexClientID}, {"code_verifier", verifier}})
}

func (s *Service) refreshOpenAITokens(ctx context.Context, refreshToken string) (protocol.JSONObject, error) {
	return s.openAITokenRequest(ctx, "refresh", [][2]string{{"grant_type", "refresh_token"}, {"refresh_token", refreshToken}, {"client_id", OpenAICodexClientID}, {"scope", "openid profile email"}})
}

func (s *Service) openAITokenRequest(ctx context.Context, operation string, pairs [][2]string) (protocol.JSONObject, error) {
	return s.desktopTokenRequest(ctx, operation, "OpenAI", OpenAIAuthIssuer+"/oauth/token", OpenAICodexTokenUserAgent, pairs)
}

func (s *Service) desktopTokenRequest(ctx context.Context, operation, providerName, endpoint, userAgent string, pairs [][2]string) (protocol.JSONObject, error) {
	settings := record()
	if s.options.Settings != nil {
		var err error
		settings, err = s.options.Settings(ctx)
		if err != nil {
			return record(), err
		}
	}
	network := networksettings.Normalize(encode(settings))
	proxy, err := networksettings.ProxyURL(network, s.options.Environment())
	if err != nil {
		return record(), err
	}
	var proxyURL *string
	if proxy != "" {
		proxyURL = &proxy
	}
	if s.options.ProxyURL != nil {
		proxyURL, err = s.options.ProxyURL(ctx)
		if err != nil {
			return record(), err
		}
	}
	client := *s.options.HTTPClient
	client.Timeout = 0
	if client.Transport == nil {
		client.Transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	if base, ok := client.Transport.(*http.Transport); ok {
		transport := base.Clone()
		if err = routeProxy(transport, proxyURL); err != nil {
			return record(), err
		}
		defer transport.CloseIdleConnections()
		client.Transport = transport
	}
	requestContext, cancel := context.WithTimeout(ctx, time.Duration(network.TimeoutMillis)*time.Millisecond)
	defer cancel()
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, formEscape(pair[0])+"="+formEscape(pair[1]))
	}
	request, err := http.NewRequestWithContext(requestContext, "POST", endpoint, strings.NewReader(strings.Join(parts, "&")))
	if err != nil {
		return record(), err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", userAgent)
	response, err := client.Do(request)
	if err != nil {
		return record(), err
	}
	defer response.Body.Close()
	data, readError := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if readError != nil {
			data = nil // Original response.text().catch returns an empty body.
		}
		body := sanitizeOpenAITokenErrorBody(string(protocol.DecodeJavaScriptUTF8(data)))
		suffix := ""
		if body != "" {
			suffix = ": " + body
		}
		return record(), fmt.Errorf("%s token %s failed: %d%s", providerName, operation, response.StatusCode, suffix)
	}
	if readError != nil {
		return record(), readError
	}
	// Response.json follows its UTF-8 JSON decoder's BOM removal. This differs
	// from the original device control path, which explicitly uses text/parse.
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON(bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(data), []byte{0xef, 0xbb, 0xbf}))
	if err != nil {
		return record(), err
	}
	var tokens protocol.JSONObject
	if err = tokens.UnmarshalJSON(normalized); err != nil || !tokens.IsObject() {
		return record(), errors.New(providerName + " token response is not an object")
	}
	return tokens, nil
}

var openAIJSONSecret = regexp.MustCompile(`(?i)"((?:access_token|refresh_token|id_token|code|code_verifier))"\s*:\s*"[^"]*"`)
var openAIFormSecret = regexp.MustCompile(`(?i)\b(access_token|refresh_token|id_token|code|code_verifier)=([^&\s]+)`)

func sanitizeOpenAITokenErrorBody(body string) string {
	body = openAIJSONSecret.ReplaceAllString(body, `"$1":"[redacted]"`)
	body = openAIFormSecret.ReplaceAllString(body, `$1=[redacted]`)
	units := utf16.Encode([]rune(body))
	if len(units) > 500 {
		units = units[:500]
	}
	return string(utf16.Decode(units))
}
