// Package nativeprovidermodels ports the provider catalog discovery used by the
// model picker. See LICENSE-cc-switch.md for the original algorithm attribution.
package nativeprovidermodels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	weburl "github.com/nlnwa/whatwg-url/url"
)

const FetchTimeout = 15 * time.Second

type Input struct {
	BaseURL, APIKey string
	IsFullURL       bool
	ModelsURL       string
}
type Model struct {
	ID      string `json:"id"`
	OwnedBy string `json:"ownedBy,omitempty"`
}
type Result struct {
	OK                           bool
	Models                       []Model
	Endpoint, ErrorCode, Message string
	HTTPStatus                   *int
	EndpointsTried               []string
}

func (result Result) MarshalJSON() ([]byte, error) {
	if result.OK {
		return json.Marshal(struct {
			OK       bool    `json:"ok"`
			Models   []Model `json:"models"`
			Endpoint string  `json:"endpoint"`
		}{true, result.Models, result.Endpoint})
	}
	return json.Marshal(struct {
		OK      bool     `json:"ok"`
		Code    string   `json:"errorCode"`
		Message string   `json:"message"`
		Status  *int     `json:"httpStatus,omitempty"`
		Tried   []string `json:"endpointsTried"`
	}{false, result.ErrorCode, result.Message, result.HTTPStatus, result.EndpointsTried})
}

type Options struct {
	Environment func() map[string]string
	Settings    func(context.Context) (protocol.JSONObject, error)
	HTTPClient  *http.Client
	// The production timeout is fixed at 15 seconds. A shorter private clock
	// permits a deterministic stalled-loopback test without provider requests.
	Timeout time.Duration
}
type Service struct{ options Options }

func New(options Options) (*Service, error) {
	if options.Environment == nil || options.Settings == nil {
		return nil, errors.New("Provider model catalog requires explicit environment and settings boundaries")
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{}
	}
	if options.Timeout <= 0 {
		options.Timeout = FetchTimeout
	}
	return &Service{options}, nil
}
func Trim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r >= '\t' && r <= '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}

var versionSegment = regexp.MustCompile(`^v\d+$`)
var compatSuffixes = []string{"/api/claudecode", "/api/anthropic", "/apps/anthropic", "/api/coding", "/claudecode", "/anthropic", "/step_plan", "/coding", "/claude"}

func assertHTTP(candidate string) error {
	parsed, err := weburl.Parse(candidate)
	if err != nil {
		return fmt.Errorf("Not a valid URL: %s", candidate)
	}
	if parsed.Scheme() != "http" && parsed.Scheme() != "https" {
		return fmt.Errorf("Unsupported URL scheme: %s:", parsed.Scheme())
	}
	return nil
}
func hasHost(value string) bool { i := strings.Index(value, "://"); return i >= 0 && len(value) > i+3 }
func BuildCandidates(base string, full bool, override string) ([]string, error) {
	if override = Trim(override); override != "" {
		if err := assertHTTP(override); err != nil {
			return nil, err
		}
		return []string{override}, nil
	}
	trimmed := strings.TrimRight(Trim(base), "/")
	if trimmed == "" {
		return nil, errors.New("Base URL is required to derive the models endpoint")
	}
	if full {
		var endpoint string
		if index := strings.Index(trimmed, "/v1/"); index >= 0 {
			endpoint = trimmed[:index] + "/v1/models"
		} else if index := strings.LastIndex(trimmed, "/"); index >= 0 && hasHost(trimmed[:index]) {
			endpoint = trimmed[:index] + "/v1/models"
		} else {
			return nil, fmt.Errorf("Cannot derive models endpoint from full URL: %s", trimmed)
		}
		if err := assertHTTP(endpoint); err != nil {
			return nil, err
		}
		return []string{endpoint}, nil
	}
	candidates := []string{}
	last := trimmed[strings.LastIndex(trimmed, "/")+1:]
	if versionSegment.MatchString(last) {
		candidates = append(candidates, trimmed+"/models")
		if !strings.HasSuffix(trimmed, "/v1") {
			candidates = append(candidates, trimmed+"/v1/models")
		}
	} else {
		candidates = append(candidates, trimmed+"/v1/models")
	}
	for _, suffix := range compatSuffixes {
		if !strings.HasSuffix(trimmed, suffix) {
			continue
		}
		root := strings.TrimSuffix(trimmed, suffix)
		if hasHost(root) {
			candidates = append(candidates, root+"/v1/models", root+"/models")
		}
		break
	}
	result := []string{}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		if err := assertHTTP(candidate); err != nil {
			return nil, err
		}
		seen[candidate] = true
		result = append(result, candidate)
	}
	return result, nil
}
func failure(code, message string, tried []string, status *int) Result {
	return Result{ErrorCode: code, Message: message, EndpointsTried: tried, HTTPStatus: status}
}
func (s *Service) Fetch(ctx context.Context, input Input) (Result, error) {
	base, key := Trim(input.BaseURL), Trim(input.APIKey)
	if base == "" || key == "" {
		return failure("missing-config", "Base URL and API key are both required to fetch models", []string{}, nil), nil
	}
	candidates, err := BuildCandidates(base, input.IsFullURL, input.ModelsURL)
	if err != nil {
		return failure("missing-config", err.Error(), []string{}, nil), nil
	}
	settings, err := s.options.Settings(ctx)
	if err != nil {
		return Result{}, err
	}
	env := s.options.Environment()
	tried := []string{}
	var lastStatus *int
	lastFailure := ""
	for _, endpoint := range candidates {
		tried = append(tried, endpoint)
		client, err := s.client(settings, env, endpoint)
		if err != nil {
			return transportFailure(err, key, tried), nil
		}
		defer client.CloseIdleConnections()
		probe, cancel := context.WithTimeout(ctx, s.options.Timeout)
		request, err := http.NewRequestWithContext(probe, "GET", endpoint, nil)
		if err != nil {
			cancel()
			return transportFailure(err, key, tried), nil
		}
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("Accept", "application/json")
		response, err := client.Do(request)
		if err != nil {
			timedOut := errors.Is(probe.Err(), context.DeadlineExceeded)
			cancel()
			if timedOut {
				return failure("timeout", "Request timed out (15s)", tried, nil), nil
			}
			return transportFailure(err, key, tried), nil
		}
		body, bodyErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		timedOut := errors.Is(probe.Err(), context.DeadlineExceeded)
		cancel()
		if bodyErr == nil {
			bodyErr = closeErr
		}
		status := response.StatusCode
		text := string(protocol.DecodeJavaScriptUTF8(body))
		if status == 404 || status == 405 {
			lastStatus = &status
			if bodyErr != nil {
				text = ""
			}
			lastFailure = describeHTTP(status, formatErrorBody(text, key))
			continue
		}
		if status < 200 || status >= 300 {
			code := "unknown"
			if status == 401 || status == 403 {
				code = "auth-failed"
			}
			if bodyErr != nil {
				text = ""
			}
			return failure(code, describeHTTP(status, formatErrorBody(text, key)), tried, &status), nil
		}
		if bodyErr != nil {
			if timedOut {
				return failure("timeout", "Request timed out (15s)", tried, nil), nil
			}
			return transportFailure(bodyErr, key, tried), nil
		}
		if !json.Valid([]byte(text)) {
			return failure("not-supported", "Endpoint did not return JSON: "+formatErrorBody(text, key), tried, &status), nil
		}
		list, found := findModelArray([]byte(text))
		if !found {
			code, message := classifyNonList([]byte(text))
			if message != "" {
				message = formatErrorBody(message, key)
			} else {
				message = "Endpoint returned an unrecognized model list: " + formatErrorBody(text, key)
			}
			return failure(code, message, tried, &status), nil
		}
		return Result{OK: true, Models: models(list), Endpoint: endpoint}, nil
	}
	return failure("endpoint-not-found", Trim("No model list endpoint found. "+lastFailure), tried, lastStatus), nil
}
func (s *Service) client(settings protocol.JSONObject, env map[string]string, endpoint string) (*http.Client, error) {
	return HTTPClientForNetwork(settings, env, endpoint, s.options.HTTPClient)
}

// HTTPClientForNetwork shares the original desktop network/proxy contract with
// provider connectivity probes. The caller closes this client's idle transport.
func HTTPClientForNetwork(settings protocol.JSONObject, env map[string]string, endpoint string, baseClient *http.Client) (*http.Client, error) {
	proxy := object(object(settings.Get("network")).Get("proxy"))
	mode := proxy.String("mode")
	selected := ""
	if mode == "manual" {
		selected = Trim(proxy.String("url"))
	} else if mode != "direct" {
		selected = Trim(env["CC_HAHA_SYSTEM_PROXY_URL"])
		if selected == "" {
			if problem := Trim(env["CC_HAHA_SYSTEM_PROXY_ERROR"]); problem != "" {
				return nil, errors.New(problem)
			}
			for _, key := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY"} {
				if env[key] != "" {
					selected = env[key]
					break
				}
			}
		}
	}
	noProxy := env["no_proxy"]
	if noProxy == "" {
		noProxy = env["NO_PROXY"]
	}
	noProxy += " localhost,127.0.0.1,::1"
	selection := provider.ResolveNativeProxySelection(endpoint, map[string]string{"https_proxy": selected, "no_proxy": noProxy})
	if baseClient == nil {
		baseClient = &http.Client{}
	}
	client := *baseClient
	client.Timeout = 0
	var transport *http.Transport
	if client.Transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	} else if original, ok := client.Transport.(*http.Transport); ok {
		transport = original.Clone()
	}
	if transport != nil {
		transport.Proxy = nil
		if selected != "" && !selection.Bypass {
			u, err := url.Parse(selected)
			if err != nil {
				return nil, err
			}
			transport.Proxy = http.ProxyURL(u)
		}
		client.Transport = transport
	}
	return &client, nil
}
func object(raw json.RawMessage) protocol.JSONObject {
	var out protocol.JSONObject
	_ = json.Unmarshal(raw, &out)
	return out
}
func findModelArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '[' {
		var out []json.RawMessage
		_ = json.Unmarshal(raw, &out)
		return out, true
	}
	record := object(raw)
	for _, key := range []string{"data", "models", "items"} {
		if value := record.Get(key); len(value) > 0 && value[0] == '[' {
			var out []json.RawMessage
			_ = json.Unmarshal(value, &out)
			return out, true
		}
	}
	return nil, false
}
func firstString(values ...json.RawMessage) string {
	for _, value := range values {
		if len(value) == 0 || value[0] != '"' {
			continue
		}
		var text string
		if json.Unmarshal(value, &text) == nil {
			if text = Trim(text); text != "" {
				return text
			}
		}
	}
	return ""
}
func models(rows []json.RawMessage) []Model {
	result := []Model{}
	seen := map[string]bool{}
	for _, row := range rows {
		entry := object(row)
		id := firstString(row, entry.Get("id"), entry.Get("slug"), entry.Get("model"), entry.Get("name"))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		owner := firstString(entry.Get("owned_by"), entry.Get("ownedBy"), entry.Get("provider"), entry.Get("vendor"), entry.Get("owner"))
		result = append(result, Model{id, owner})
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, right := utf16.Encode([]rune(result[i].ID)), utf16.Encode([]rune(result[j].ID))
		for k := 0; k < min(len(left), len(right)); k++ {
			if left[k] != right[k] {
				return left[k] < right[k]
			}
		}
		return len(left) < len(right)
	})
	return result
}

var authHint = regexp.MustCompile(`(?i)auth|unauthor|invalid_api_key`)

func classifyNonList(raw json.RawMessage) (string, string) {
	record := object(raw)
	errorObject := object(record.Get("error"))
	auth := false
	for _, row := range []protocol.JSONObject{record, errorObject} {
		for _, key := range []string{"code", "status"} {
			var value any
			_ = json.Unmarshal(row.Get(key), &value)
			if value == float64(401) || value == float64(403) || value == "401" || value == "403" {
				auth = true
			}
		}
		for _, key := range []string{"type", "code"} {
			var value string
			if json.Unmarshal(row.Get(key), &value) == nil && authHint.MatchString(value) {
				auth = true
			}
		}
	}
	code := "not-supported"
	if auth {
		code = "auth-failed"
	}
	return code, firstString(record.Get("msg"), record.Get("message"), errorObject.Get("msg"), errorObject.Get("message"))
}
func redactKey(text, key string) string {
	if len(utf16.Encode([]rune(key))) < 8 {
		return text
	}
	return strings.ReplaceAll(text, key, "***")
}
func formatErrorBody(text, key string) string {
	text = redactKey(text, key)
	units := utf16.Encode([]rune(text))
	if len(units) > 512 {
		return string(utf16.Decode(units[:512])) + "…"
	}
	return text
}
func describeHTTP(status int, body string) string {
	if body == "" {
		return fmt.Sprintf("HTTP %d", status)
	}
	return fmt.Sprintf("HTTP %d: %s", status, body)
}
func transportFailure(err error, key string, tried []string) Result {
	var urlError *url.Error
	if errors.As(err, &urlError) {
		err = urlError.Err
	}
	return failure("network", redactKey(err.Error(), key), tried, nil)
}
