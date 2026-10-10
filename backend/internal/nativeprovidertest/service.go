// Package nativeprovidertest performs the original direct and proxy-pipeline
// connectivity checks with real HTTP exchanges. See LICENSE-cc-switch.md.
package nativeprovidertest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativeprovidermodels"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
)

type Input struct {
	BaseURL, APIKey, ModelID, APIFormat, AuthStrategy, PresetID string
	SupportsNestedMedia                                         *bool
	RequestCompatibility                                        protocol.JSONObject
}
type Step struct {
	Success    bool   `json:"success"`
	LatencyMS  int64  `json:"latencyMs"`
	Error      string `json:"error,omitempty"`
	ModelUsed  string `json:"modelUsed,omitempty"`
	HTTPStatus *int   `json:"httpStatus,omitempty"`
}
type Result struct {
	Connectivity Step  `json:"connectivity"`
	Proxy        *Step `json:"proxy,omitempty"`
}
type Options struct {
	Environment     func() map[string]string
	Settings        func(context.Context) (protocol.JSONObject, error)
	Provider        func(context.Context, string) (protocol.JSONObject, error)
	HTTPClient      *http.Client
	Version         string
	Now             func() time.Time
	ProbeSessionID  func() (string, error)
	TimeoutOverride time.Duration
}
type Service struct{ options Options }

func New(options Options) (*Service, error) {
	if options.Environment == nil || options.Settings == nil {
		return nil, errors.New("Provider connection checks require explicit settings and environment")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Version == "" {
		options.Version = "dev"
	}
	if options.ProbeSessionID == nil {
		options.ProbeSessionID = probeSessionID
	}
	return &Service{options}, nil
}
func object(raw json.RawMessage) protocol.JSONObject {
	var out protocol.JSONObject
	_ = json.Unmarshal(raw, &out)
	return out
}
func encoded(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func record(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func field(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: encoded(value)}
}
func probeSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b[:])
	return "cc-haha-probe-" + h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

var modelSuffix = regexp.MustCompile(`(?i)\[(1|2)m\]`)

func preset(id string) protocol.JSONObject {
	var rows []protocol.JSONObject
	_ = json.Unmarshal(providerconfig.Presets(), &rows)
	for _, row := range rows {
		if row.String("id") == id {
			return row
		}
	}
	return record()
}
func formats(providerFormat, model string, p protocol.JSONObject) (string, string) {
	if providerFormat == "" {
		providerFormat = "anthropic"
	}
	var rules []protocol.JSONObject
	_ = json.Unmarshal(p.Get("modelApiFormats"), &rules)
	if len(rules) > 0 && p.String("apiFormat") != "" {
		providerFormat = p.String("apiFormat")
	}
	model = strings.ToLower(nativeprovidermodels.Trim(model))
	for _, rule := range rules {
		var prefixes []string
		_ = json.Unmarshal(rule.Get("prefixes"), &prefixes)
		for _, prefix := range prefixes {
			prefix = strings.ToLower(nativeprovidermodels.Trim(prefix))
			if prefix != "" && strings.HasPrefix(model, prefix) {
				return providerFormat, rule.String("apiFormat")
			}
		}
	}
	return providerFormat, providerFormat
}
func (s *Service) Saved(ctx context.Context, id, override string) (Result, error) {
	if s.options.Provider == nil {
		return Result{}, errors.New("Saved provider loading is unavailable")
	}
	provider, err := s.options.Provider(ctx, id)
	if err != nil {
		return Result{}, err
	}
	p := preset(provider.String("presetId"))
	strategy := provider.String("authStrategy")
	if strategy == "" {
		strategy = p.String("authStrategy")
		if strategy == "" {
			strategy = "auth_token"
		}
	}
	key := provider.String("apiKey")
	defaults := object(p.Get("defaultEnv"))
	if key == "" {
		key = defaults.String("ANTHROPIC_AUTH_TOKEN")
	}
	if key == "" {
		key = defaults.String("ANTHROPIC_API_KEY")
	}
	if key == "" && strategy == "dual_dummy" {
		key = "dummy"
	}
	if provider.String("baseUrl") == "" || key == "" {
		return Result{Connectivity: Step{Error: "Missing baseUrl or apiKey"}}, nil
	}
	model := object(provider.Get("models")).String("main")
	if override != "" {
		model = override
	}
	_, format := formats(provider.String("apiFormat"), model, p)
	var nested *bool
	if v := provider.Get("supportsNestedToolResultMedia"); v != nil && string(v) != "null" {
		var value bool
		if json.Unmarshal(v, &value) == nil {
			nested = &value
		}
	}
	return s.Test(ctx, Input{BaseURL: provider.String("baseUrl"), APIKey: key, ModelID: model, APIFormat: format, AuthStrategy: strategy, PresetID: provider.String("presetId"), SupportsNestedMedia: nested, RequestCompatibility: object(provider.Get("requestCompatibility"))})
}
func (s *Service) Test(ctx context.Context, input Input) (Result, error) {
	settings, err := s.options.Settings(ctx)
	if err != nil {
		return Result{}, err
	}
	env := s.options.Environment()
	model := modelSuffix.ReplaceAllString(input.ModelID, "")
	base := strings.TrimRight(input.BaseURL, "/")
	strategy := input.AuthStrategy
	if strategy == "" {
		strategy = "api_key"
	}
	p := preset(input.PresetID)
	providerFormat, format := formats(input.APIFormat, model, p)
	session, err := s.options.ProbeSessionID()
	if err != nil {
		return Result{}, err
	}
	headers := s.upstreamHeaders(p, session)
	timeout := networkTimeout(settings)
	result := Result{Connectivity: s.step(ctx, base, input.APIKey, model, format, strategy, settings, env, timeout, input.RequestCompatibility, headers, false)}
	if !result.Connectivity.Success {
		return result, nil
	}
	needsProxy := providerFormat != "anthropic" || input.SupportsNestedMedia != nil && !*input.SupportsNestedMedia
	if needsProxy {
		step := s.step(ctx, base, input.APIKey, model, format, strategy, settings, env, timeout, input.RequestCompatibility, headers, true)
		result.Proxy = &step
	}
	return result, nil
}
func (s *Service) upstreamHeaders(p protocol.JSONObject, session string) http.Header {
	headers := http.Header{}
	template := object(p.Get("upstreamHeaders"))
	blocked := map[string]bool{"authorization": true, "x-api-key": true, "host": true, "content-type": true, "content-length": true, "connection": true}
	for _, property := range template.OwnProperties() {
		var name, value string
		_ = json.Unmarshal(property.NameJSON, &name)
		_ = json.Unmarshal(property.Value, &value)
		name = nativeprovidermodels.Trim(name)
		if name == "" || blocked[strings.ToLower(name)] {
			continue
		}
		if strings.Contains(value, "$SESSION_ID") && nativeprovidermodels.Trim(session) == "" {
			continue
		}
		value = nativeprovidermodels.Trim(strings.ReplaceAll(strings.ReplaceAll(value, "$SESSION_ID", nativeprovidermodels.Trim(session)), "$VERSION", s.options.Version))
		if value == "" || strings.ContainsAny(value, "\r\n") {
			continue
		}
		headers.Set(name, value)
	}
	return headers
}
func networkTimeout(settings protocol.JSONObject) time.Duration {
	var milliseconds float64
	if json.Unmarshal(object(settings.Get("network")).Get("aiRequestTimeoutMs"), &milliseconds) != nil || math.IsNaN(milliseconds) || math.IsInf(milliseconds, 0) {
		milliseconds = 1800000
	}
	milliseconds = math.Min(math.Max(math.Floor(milliseconds+.5), 30000), 2147483000)
	return time.Duration(milliseconds) * time.Millisecond
}
func (s *Service) step(ctx context.Context, base, key, model, format, strategy string, settings protocol.JSONObject, env map[string]string, timeout time.Duration, compat protocol.JSONObject, extra http.Header, pipeline bool) Step {
	start := s.options.Now()
	step := Step{ModelUsed: model}
	finish := func(failure string) Step {
		step.LatencyMS = s.options.Now().UnixMilli() - start.UnixMilli()
		step.Error = failure
		return step
	}
	endpoint, headers, body, err := buildRequest(base, key, model, format, strategy, compat, extra, pipeline)
	if err != nil {
		return finish(err.Error())
	}
	client, err := nativeprovidermodels.HTTPClientForNetwork(settings, env, endpoint, s.options.HTTPClient)
	if err != nil {
		return finish(err.Error())
	}
	defer client.CloseIdleConnections()
	budget := timeout
	if s.options.TimeoutOverride > 0 {
		budget = s.options.TimeoutOverride
	}
	probe, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	request, err := http.NewRequestWithContext(probe, "POST", endpoint, bytes.NewReader(encoded(body)))
	if err != nil {
		return finish(err.Error())
	}
	request.Header = headers
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(probe.Err(), context.DeadlineExceeded) {
			label := "Request"
			if pipeline {
				label = "Proxy pipeline"
			}
			return finish(fmt.Sprintf("%s timed out (%ds)", label, int64(math.Floor(timeout.Seconds()+.5))))
		}
		var u *url.Error
		if errors.As(err, &u) {
			err = u.Err
		}
		return finish(err.Error())
	}
	defer response.Body.Close()
	step.LatencyMS = s.options.Now().UnixMilli() - start.UnixMilli()
	status := response.StatusCode
	raw, readErr := io.ReadAll(response.Body)
	raw = protocol.DecodeJavaScriptUTF8(raw)
	payload := object(raw)
	if !pipeline {
		if readErr != nil || !json.Valid(raw) {
			payload = record()
		}
		step.HTTPStatus = &status
		if status < 200 || status >= 300 {
			step.Error = fmt.Sprintf("HTTP %d", status)
			if message := object(payload.Get("error")).String("message"); message != "" {
				step.Error = message
			}
			return step
		}
		if failure := validateDirect(raw, payload, format, readErr); failure != "" {
			step.Error = failure
			return step
		}
		step.Success = true
		if payload.String("model") != "" {
			step.ModelUsed = payload.String("model")
		}
		return step
	}
	if status < 200 || status >= 300 {
		step.HTTPStatus = &status
		text := ""
		if readErr == nil {
			text = substring(string(raw), 200)
		}
		step.Error = fmt.Sprintf("Upstream HTTP %d: %s", status, text)
		return step
	}
	if readErr != nil {
		if errors.Is(probe.Err(), context.DeadlineExceeded) {
			return finish(fmt.Sprintf("Proxy pipeline timed out (%ds)", int64(math.Floor(timeout.Seconds()+.5))))
		}
		return finish(readErr.Error())
	}
	if !json.Valid(raw) {
		return finish("Invalid upstream JSON response")
	}
	converted := payload
	if format != "anthropic" {
		converted, err = TransformResponse(raw, model, format, s.options.Now())
		if err != nil {
			return finish(err.Error())
		}
	}
	step.LatencyMS = s.options.Now().UnixMilli() - start.UnixMilli()
	if converted.String("type") != "message" || !isArray(converted.Get("content")) {
		step.Error = "Proxy transform produced invalid Anthropic response"
		return step
	}
	step.Success = true
	step.HTTPStatus = &status
	if converted.String("model") != "" {
		step.ModelUsed = converted.String("model")
	}
	return step
}
func isArray(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '['
}
func validateDirect(raw json.RawMessage, payload protocol.JSONObject, format string, readErr error) string {
	if readErr != nil || !json.Valid(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "Empty response — not a valid API endpoint"
	}
	if errorPayload := object(payload.Get("error")); errorPayload.IsObject() {
		message := errorPayload.String("message")
		if message != "" {
			return message
		}
		return "Error in response body"
	}
	switch format {
	case "openai_chat":
		var choices []json.RawMessage
		if json.Unmarshal(payload.Get("choices"), &choices) != nil || len(choices) == 0 {
			return "Response missing choices — not a valid Chat Completions endpoint"
		}
	case "openai_responses":
		if !isArray(payload.Get("output")) {
			return "Response missing output — not a valid Responses API endpoint"
		}
	default:
		if payload.String("type") != "message" || !isArray(payload.Get("content")) {
			return "Not a valid Anthropic Messages endpoint"
		}
	}
	return ""
}
