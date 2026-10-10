// Package websearch owns the Source web-search selection, external HTTP
// providers and native-result reducer. Model execution remains owner-bound.
package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

type Config struct{ Mode, TavilyAPIKey, BraveAPIKey string }
type Options struct {
	Client   *http.Client
	Settings func() (protocol.JSONObject, error)
	Now      func() time.Time
}
type Service struct {
	options     Options
	mu          sync.Mutex
	unsupported map[string]bool
}
type Output struct {
	Query           json.RawMessage   `json:"query"`
	Results         []json.RawMessage `json:"results"`
	DurationSeconds float64           `json:"durationSeconds"`
}
type Progress func(json.RawMessage, protocol.JSONObject) error
type Native func(context.Context, protocol.JSONObject, Progress) ([]protocol.JSONObject, error)
type External func(context.Context, string, protocol.JSONObject, string) (Output, error)

func New(o Options) (*Service, error) {
	if o.Client == nil || o.Settings == nil || o.Now == nil {
		return nil, errors.New("WebSearch requires actual HTTP/settings/clock producers")
	}
	return &Service{options: o, unsupported: map[string]bool{}}, nil
}
func quote(value string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(value)))
}
func raw(value any) json.RawMessage            { b, _ := json.Marshal(value); return b }
func field(k string, v any) protocol.JSONField { return protocol.JSONField{Name: k, Value: raw(v)} }
func jsTrim(v string) string {
	return strings.TrimFunc(v, func(r rune) bool {
		return r >= 9 && r <= 13 || r == 32 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}
func ConfigFromSettings(settings protocol.JSONObject) Config {
	var web protocol.JSONObject
	if json.Unmarshal(settings.Get("webSearch"), &web) != nil || !web.IsObject() {
		return Config{}
	}
	mode := web.String("mode")
	switch mode {
	case "auto", "anthropic", "tavily", "brave", "disabled":
	default:
		mode = "auto"
	}
	return Config{mode, jsTrim(web.String("tavilyApiKey")), jsTrim(web.String("braveApiKey"))}
}
func IsLikelyClaudeModel(model string) bool {
	return regexp.MustCompile(`(^|[/:._-])claude([/:._-]|$)`).MatchString(strings.ToLower(model))
}
func (s *Service) Resolve(model string, c Config) string {
	s.mu.Lock()
	unsupported := s.unsupported[strings.ToLower(jsTrim(model))]
	s.mu.Unlock()
	native := IsLikelyClaudeModel(model) && !unsupported
	switch c.Mode {
	case "disabled":
		return "disabled"
	case "tavily":
		if c.TavilyAPIKey != "" {
			return "tavily"
		}
		return "disabled"
	case "brave":
		if c.BraveAPIKey != "" {
			return "brave"
		}
		return "disabled"
	case "anthropic":
		if native {
			return "anthropic"
		}
		return "disabled"
	}
	if native {
		return "anthropic"
	}
	if c.TavilyAPIKey != "" {
		return "tavily"
	}
	if c.BraveAPIKey != "" {
		return "brave"
	}
	return "disabled"
}
func (s *Service) Enabled(model string) (bool, error) {
	settings, e := s.options.Settings()
	if e != nil {
		return false, e
	}
	return s.Resolve(model, ConfigFromSettings(settings)) != "disabled", nil
}
func ShouldFallback(errorText string) bool {
	return regexp.MustCompile(`\b(400|422)\b`).MatchString(errorText) || regexp.MustCompile(`(?i)web_search|server tool|tool schema|input_schema|extra input|unsupported`).MatchString(errorText)
}
func (s *Service) Call(ctx context.Context, input protocol.JSONObject, model string, native Native, external External, progress Progress) (Output, error) {
	start := s.options.Now()
	settings, e := s.options.Settings()
	if e != nil {
		return Output{}, e
	}
	config := ConfigFromSettings(settings)
	provider := s.Resolve(model, config)
	query := input.Get("query")
	unavailable := func(reason string) Output {
		return Output{query, []json.RawMessage{quote(reason)}, s.options.Now().Sub(start).Seconds()}
	}
	if provider == "disabled" {
		return unavailable("Web search is not configured for this model. Use a Claude model for native web search or add a Tavily/Brave API key in Settings."), nil
	}
	if provider == "tavily" || provider == "brave" {
		if progress != nil {
			if e = progress(quote(provider+"-web-search"), protocol.NewJSONObject(field("type", "query_update"), protocol.JSONField{Name: "query", Value: query})); e != nil {
				return Output{}, e
			}
		}
		key := config.TavilyAPIKey
		if provider == "brave" {
			key = config.BraveAPIKey
		}
		if external == nil {
			return Output{}, errors.New("WebSearch external provider not bound")
		}
		out, e := external(ctx, provider, input, key)
		if e != nil {
			return Output{}, e
		}
		if progress != nil {
			count := 0
			if len(out.Results) > 1 {
				var result protocol.JSONObject
				var hits []json.RawMessage
				_ = json.Unmarshal(out.Results[1], &result)
				_ = json.Unmarshal(result.Get("content"), &hits)
				count = len(hits)
			}
			e = progress(quote(provider+"-web-search"), protocol.NewJSONObject(field("type", "search_results_received"), field("resultCount", count), protocol.JSONField{Name: "query", Value: query}))
		}
		return out, e
	}
	if native == nil {
		return Output{}, errors.New("WebSearch native model producer not bound")
	}
	blocks, e := native(ctx, input, progress)
	if e == nil {
		return NativeOutput(blocks, query, s.options.Now().Sub(start).Seconds()), nil
	}
	if !ShouldFallback(e.Error()) {
		return Output{}, e
	}
	s.mu.Lock()
	key := strings.ToLower(jsTrim(model))
	if key != "" {
		s.unsupported[key] = true
	}
	s.mu.Unlock()
	fallback := ""
	apiKey := ""
	if config.TavilyAPIKey != "" {
		fallback, apiKey = "tavily", config.TavilyAPIKey
	} else if config.BraveAPIKey != "" {
		fallback, apiKey = "brave", config.BraveAPIKey
	}
	if fallback == "" {
		return unavailable("Native Anthropic web search failed for this endpoint, and no Tavily/Brave API key is configured for fallback."), nil
	}
	if external == nil {
		return Output{}, errors.New("WebSearch fallback provider not bound")
	}
	return external(ctx, fallback, input, apiKey)
}
func (s *Service) SearchExternal(ctx context.Context, provider string, input protocol.JSONObject, key string) (Output, error) {
	start := s.options.Now()
	var request *http.Request
	var e error
	switch provider {
	case "tavily":
		body := protocol.NewJSONObject(protocol.JSONField{Name: "query", Value: input.Get("query")}, field("max_results", 8), field("search_depth", "basic"), field("include_answer", false), protocol.JSONField{Name: "include_domains", Value: input.Get("allowed_domains")}, protocol.JSONField{Name: "exclude_domains", Value: input.Get("blocked_domains")})
		request, e = http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(raw(body)))
		if e == nil {
			request.Header.Set("Authorization", "Bearer "+key)
			request.Header.Set("Content-Type", "application/json")
		}
	case "brave":
		q, e := DomainFilteredQuery(input)
		if e != nil {
			return Output{}, e
		}
		params := "q=" + strings.ReplaceAll(url.QueryEscape(q), "~", "%7E") + "&count=8"
		request, e = http.NewRequestWithContext(ctx, "GET", "https://api.search.brave.com/res/v1/web/search?"+params, nil)
		if e == nil {
			request.Header.Set("Accept", "application/json")
			request.Header.Set("X-Subscription-Token", key)
		}
	default:
		return Output{}, errors.New("Unknown external WebSearch provider " + provider)
	}
	if e != nil {
		return Output{}, e
	}
	request.Header.Set("Accept-Encoding", "gzip, deflate, br")
	response, e := s.options.Client.Do(request)
	if e != nil {
		return Output{}, e
	}
	defer response.Body.Close()
	body, e := webfetch.ReadDecodedResponse(response)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if e != nil {
			body = nil
		}
		text := utf16.Encode([]rune(string(protocol.DecodeJavaScriptUTF8(body))))
		if len(text) > 500 {
			text = text[:500]
		}
		name := "Tavily"
		if provider == "brave" {
			name = "Brave"
		}
		return Output{}, errors.New(fmt.Sprintf("%s search failed: %d %s", name, response.StatusCode, string(utf16.Decode(text))))
	}
	if e != nil {
		return Output{}, e
	}
	var object protocol.JSONObject
	if e = json.Unmarshal(body, &object); e != nil {
		return Output{}, e
	}
	if provider == "brave" {
		var nested protocol.JSONObject
		_ = json.Unmarshal(object.Get("web"), &nested)
		object = nested
	}
	var rows []protocol.JSONObject
	if value := object.Get("results"); value != nil && string(value) != "null" {
		if e = json.Unmarshal(value, &rows); e != nil {
			return Output{}, e
		}
	}
	hits := []protocol.JSONObject{}
	for _, row := range rows {
		if _, e := protocol.DecodeJavaScriptJSONString(row.Get("title")); e != nil {
			continue
		}
		if _, e := protocol.DecodeJavaScriptJSONString(row.Get("url")); e != nil {
			continue
		}
		hits = append(hits, protocol.NewJSONObject(protocol.JSONField{Name: "title", Value: row.Get("title")}, protocol.JSONField{Name: "url", Value: row.Get("url")}))
	}
	return Output{input.Get("query"), []json.RawMessage{quote("Search provider: " + provider), raw(protocol.NewJSONObject(field("tool_use_id", provider+"-web-search"), field("content", hits)))}, s.options.Now().Sub(start).Seconds()}, nil
}
func DomainFilteredQuery(input protocol.JSONObject) (string, error) {
	parts := []json.RawMessage{}
	for _, key := range []string{"allowed_domains", "blocked_domains"} {
		var domains []json.RawMessage
		_ = json.Unmarshal(input.Get(key), &domains)
		valid := []json.RawMessage{}
		for _, domain := range domains {
			units, e := protocol.DecodeJavaScriptJSONString(domain)
			if e != nil {
				return "", e
			}
			if len(units) > 0 {
				prefix := "site:"
				if key == "blocked_domains" {
					prefix = "-site:"
				}
				value, _ := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote(prefix), domain}, "")
				valid = append(valid, value)
			}
		}
		if len(valid) > 0 {
			sep := " OR "
			if key == "blocked_domains" {
				sep = " "
			}
			clause, _ := protocol.JoinJavaScriptValuesJSON(valid, sep)
			if key == "allowed_domains" {
				clause, _ = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("("), clause, quote(") ")}, "")
			} else {
				clause, _ = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{clause, quote(" ")}, "")
			}
			parts = append(parts, clause)
		}
	}
	parts = append(parts, input.Get("query"))
	joined, e := protocol.JoinJavaScriptValuesJSON(parts, "")
	if e != nil {
		return "", e
	}
	units, e := protocol.DecodeJavaScriptJSONString(joined)
	return jsTrim(string(utf16.Decode(units))), e
}
