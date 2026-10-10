package providerproxy

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ProxyConfig struct {
	ID                            string          `json:"id"`
	Name                          string          `json:"name"`
	BaseURL                       string          `json:"baseUrl"`
	APIKey                        string          `json:"apiKey"`
	APIFormat                     string          `json:"apiFormat"`
	AuthStrategy                  string          `json:"authStrategy"`
	SupportsNestedToolResultMedia bool            `json:"supportsNestedToolResultMedia"`
	ModelAPIFormats               json.RawMessage `json:"modelApiFormats"`
	UpstreamHeaders               json.RawMessage `json:"upstreamHeaders"`
	RequestCompatibility          json.RawMessage `json:"requestCompatibility,omitempty"`
}
type ProxyRequest struct {
	Model, Protocol, URL string
	Stream               bool
	Anthropic, Upstream  protocol.JSONObject
	Headers              http.Header
	OutputBudget         protocol.JSONObject
}

var proxyDeepSeek = regexp.MustCompile(`(?i)(^|[./-])deepseek([./-]|$)`)
var proxyOpenCode = regexp.MustCompile(`(?i)(^|[./-])opencode\.ai([:/]|$)`)
var proxyVision = regexp.MustCompile(`(?i)(^|[/:._-])vision([/:._-]|$)`)
var proxyCLIUserAgent = regexp.MustCompile(`(?i)^claude-cli/[^\s]+\s+\(`)

func ResolveProxyUpstreamHeaders(template json.RawMessage, sessionID, version string) http.Header {
	headers := http.Header{}
	sessionID = protocol.TrimJavaScriptString(sessionID)
	for _, field := range responseObject(template).OwnProperties() {
		name := protocol.TrimJavaScriptString(responseText(field.NameJSON))
		lower := strings.ToLower(name)
		switch lower {
		case "", "authorization", "x-api-key", "host", "content-type", "content-length", "connection":
			continue
		}
		value := responseText(field.Value)
		if strings.Contains(value, "$SESSION_ID") && sessionID == "" {
			continue
		}
		value = protocol.TrimJavaScriptString(strings.ReplaceAll(strings.ReplaceAll(value, "$SESSION_ID", sessionID), "$VERSION", version))
		if value == "" || strings.ContainsAny(value, "\r\n") {
			continue
		}
		headers.Set(name, value)
	}
	return headers
}
func ProxyPromptCacheKey(body protocol.JSONObject, sessionID string) json.RawMessage {
	metadata := responseObject(body.Get("metadata"))
	user := metadata.Get("user_id")
	if responseString(user) {
		units := responseUnits(user)
		marker := []uint16{'_', 's', 'e', 's', 's', 'i', 'o', 'n', '_'}
		for i := 0; i+len(marker) <= len(units); i++ {
			if equalResponseUnits(units[i:i+len(marker)], marker) {
				if suffix := units[i+len(marker):]; len(suffix) > 0 {
					return protocol.QuoteJavaScriptUTF16(suffix)
				}
				break
			}
		}
	}
	if id := metadata.Get("session_id"); responseString(id) && responseTruthy(id) {
		return id
	}
	if id := protocol.TrimJavaScriptString(sessionID); id != "" {
		return responseJSON(id)
	}
	return nil
}
func ResolveProxyModelFormat(rules json.RawMessage, model string) string {
	model = strings.ToLower(protocol.TrimJavaScriptString(model))
	if model == "" {
		return ""
	}
	for _, row := range requestArray(rules) {
		rule := responseObject(row)
		for _, prefix := range requestArray(rule.Get("prefixes")) {
			needle := strings.ToLower(protocol.TrimJavaScriptString(responseText(prefix)))
			if needle != "" && strings.HasPrefix(model, needle) {
				return rule.String("apiFormat")
			}
		}
	}
	return ""
}
func ProxyHopHeaders(headers http.Header) map[string]bool {
	deny := map[string]bool{}
	for _, name := range []string{"connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade", "proxy-connection"} {
		deny[name] = true
	}
	for _, token := range strings.Split(headers.Get("Connection"), ",") {
		if name := strings.ToLower(protocol.TrimJavaScriptString(token)); name != "" {
			deny[name] = true
		}
	}
	return deny
}
func StripProxyHopHeaders(headers http.Header) http.Header {
	result := http.Header{}
	deny := ProxyHopHeaders(headers)
	for name, values := range headers {
		if !deny[strings.ToLower(name)] {
			result[name] = append([]string(nil), values...)
		}
	}
	return result
}

// NormalizeProxyModel runs before the original handler's conversion/fetch try
// block. Keep its TypeError distinct from caught upstream/conversion failures.
func NormalizeProxyModel(modelRaw json.RawMessage) (json.RawMessage, error) {
	if err := mediaPropertyError(modelRaw, "model.replace"); err != nil {
		return nil, err
	}
	if !responseString(modelRaw) {
		failure := responseError(`model.replace is not a function. (In 'model.replace(/\[(1|2)m\]/gi, "")', 'model.replace' is undefined)`)
		failure.Name = "TypeError"
		return nil, failure
	}
	units := responseUnits(modelRaw)
	normalized := []uint16{}
	for i := 0; i < len(units); i++ {
		if i+3 < len(units) && units[i] == '[' && (units[i+1] == '1' || units[i+1] == '2') && (units[i+2] == 'm' || units[i+2] == 'M') && units[i+3] == ']' {
			i += 3
			continue
		}
		normalized = append(normalized, units[i])
	}
	return protocol.QuoteJavaScriptUTF16(normalized), nil
}

func PrepareProxyRequest(config ProxyConfig, body protocol.JSONObject, incoming http.Header, version string, scoped bool) (ProxyRequest, error) {
	modelRaw, err := NormalizeProxyModel(body.Get("model"))
	if err != nil {
		return ProxyRequest{}, err
	}
	model := responseText(modelRaw)
	body = body.Clone()
	body.Set("model", modelRaw)
	format := ResolveProxyModelFormat(config.ModelAPIFormats, model)
	overridden := format != ""
	if !overridden {
		format = config.APIFormat
	}
	base := strings.TrimRight(config.BaseURL, "/")
	p := ProxyRequest{Model: model, Protocol: format, Stream: string(body.Get("stream")) == "true", Anthropic: body, Headers: http.Header{}}
	extra := ResolveProxyUpstreamHeaders(config.UpstreamHeaders, incoming.Get("x-claude-code-session-id"), version)
	options := responseRecord(responseRawField("requestCompatibility", config.RequestCompatibility), responseField("budgetSource", "explicit"))
	if incoming.Get("x-cc-haha-output-budget-source") == "default" {
		options.Set("budgetSource", responseJSON("default"))
	}
	if format == "anthropic" {
		if !overridden && config.SupportsNestedToolResultMedia {
			message := "Active provider uses anthropic format — proxy not needed"
			if scoped {
				message = `Provider "` + config.ID + `" uses anthropic format — proxy not needed`
			}
			return ProxyRequest{}, &ResponseError{Message: message, Status: 400, Type: "invalid_request_error"}
		}
		p.Upstream = body
		if ShouldHoistNestedToolResultMedia(responseRecord(responseField("providerApiFormat", config.APIFormat), responseField("resolvedApiFormat", format), responseField("supportsNestedToolResultMedia", config.SupportsNestedToolResultMedia))) {
			var err error
			p.Upstream, err = HoistToolResultMediaForCompatibility(body)
			if err != nil {
				return ProxyRequest{}, err
			}
		}
		base = strings.TrimSuffix(base, "/v1")
		p.URL = base + "/v1/messages"
		p.Headers.Set("Content-Type", "application/json")
		switch config.AuthStrategy {
		case "dual_dummy":
			p.Headers.Set("x-api-key", "dummy")
			p.Headers.Set("Authorization", "Bearer dummy")
		case "auth_token", "auth_token_empty_api_key":
			if config.APIKey != "" {
				p.Headers.Set("Authorization", "Bearer "+config.APIKey)
			}
		case "dual_same_token":
			if config.APIKey != "" {
				p.Headers.Set("x-api-key", config.APIKey)
				p.Headers.Set("Authorization", "Bearer "+config.APIKey)
			}
		default:
			if config.APIKey != "" {
				p.Headers.Set("x-api-key", config.APIKey)
			}
		}
		deny := ProxyHopHeaders(incoming)
		for name, values := range incoming {
			lower := strings.ToLower(name)
			value := strings.Join(values, ", ")
			if deny[lower] {
				continue
			}
			switch lower {
			case "host", "content-type", "content-length", "x-api-key", "authorization", "x-cc-haha-output-budget-source", "x-claude-code-session-id", "x-claude-remote-container-id", "x-claude-remote-session-id", "x-client-app":
				continue
			case "x-app":
				if value == "cli" {
					continue
				}
			case "user-agent":
				if proxyCLIUserAgent.MatchString(value) {
					continue
				}
			}
			if value != "" {
				p.Headers.Set(name, value)
			}
		}
		for name, values := range extra {
			p.Headers[name] = values
		}
		return p, nil
	}
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	for name, values := range extra {
		p.Headers[name] = values
	}
	p.Headers.Set("Content-Type", "application/json")
	p.Headers.Set("Authorization", "Bearer "+config.APIKey)
	compatOptions := options.Clone()
	compatOptions.Set("protocol", responseJSON(format))
	compat, err := ResolveRequestCompatibility(body, compatOptions)
	if err != nil {
		return ProxyRequest{}, err
	}
	p.OutputBudget = responseObject(compat.Get("outputBudget"))
	if format == "openai_chat" {
		p.URL = base + "/chat/completions"
		known := proxyDeepSeek.MatchString(base) || proxyOpenCode.MatchString(base)
		// The model reasoning profile includes DeepSeek V4 names on other hosts.
		reasoningModel := strings.ToLower(protocol.TrimJavaScriptString(model))
		reasoningModel = strings.TrimSuffix(strings.TrimSuffix(reasoningModel, "[1m]"), ":1m")
		if i := strings.LastIndex(reasoningModel, "/"); i >= 0 {
			reasoningModel = reasoningModel[i+1:]
		}
		deepSeekFamily := strings.HasPrefix(reasoningModel, "deepseek-v4") || reasoningModel == "deepseek-chat" || reasoningModel == "deepseek-reasoner"
		options.Set("roundTripReasoningContent", responseJSON(known || deepSeekFamily))
		options.Set("passThinkingToggle", responseJSON(known))
		textOnly := proxyDeepSeek.MatchString(base) && !proxyVision.MatchString(model) || proxyOpenCode.MatchString(base) && !proxyVision.MatchString(model) && strings.ToLower(model) != "kimi-k3" && strings.ToLower(model) != "space-bunny-free"
		mode := "vision"
		if textOnly {
			mode = "text_only"
		}
		options.Set("imageContentMode", responseJSON(mode))
		p.Upstream, err = AnthropicToChat(body, options)
	} else {
		p.URL = base + "/responses"
		options.Set("cacheKey", ProxyPromptCacheKey(body, incoming.Get("x-claude-code-session-id")))
		p.Upstream, err = AnthropicToResponses(body, options)
	}
	return p, err
}
