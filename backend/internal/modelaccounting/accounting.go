package modelaccounting

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"strings"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "model accounting capability is not migrated: " + e.Boundary
}
func unsupported(b string) error { return &CapabilityError{b} }

func quote(v string) json.RawMessage { r, _ := json.Marshal(v); return r }
func raw(v any) json.RawMessage      { r, _ := json.Marshal(v); return r }
func object(r json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(r)
	return o
}
func num(r json.RawMessage) float64 { var n float64; _ = json.Unmarshal(r, &n); return n }
func jsonBytes(o protocol.JSONObject) ([]byte, error) {
	r, e := o.MarshalJSON()
	if e != nil {
		return nil, e
	}
	return protocol.ParseAndStringifyJavaScriptJSON(r)
}
func ZeroUsage() protocol.JSONObject {
	return protocol.NewJSONObject(
		protocol.JSONField{Name: "input_tokens", Value: raw(0)}, protocol.JSONField{Name: "cache_creation_input_tokens", Value: raw(0)},
		protocol.JSONField{Name: "cache_read_input_tokens", Value: raw(0)}, protocol.JSONField{Name: "output_tokens", Value: raw(0)},
		protocol.JSONField{Name: "server_tool_use", Value: json.RawMessage(`{"web_search_requests":0,"web_fetch_requests":0}`)},
		protocol.JSONField{Name: "service_tier", Value: quote("standard")},
		protocol.JSONField{Name: "cache_creation", Value: json.RawMessage(`{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0}`)},
		protocol.JSONField{Name: "inference_geo", Value: quote("")}, protocol.JSONField{Name: "iterations", Value: raw([]any{})}, protocol.JSONField{Name: "speed", Value: quote("standard")})
}

// UpdateUsage retains cumulative stream totals and the Source's >0 input guard.
func UpdateUsage(previous, part protocol.JSONObject) (protocol.JSONObject, error) {
	if !part.IsObject() {
		return previous.Clone(), nil
	}
	for _, property := range part.OwnProperties() {
		var key string
		_ = json.Unmarshal(property.NameJSON, &key)
		switch key {
		case "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "server_tool_use", "cache_creation", "service_tier", "inference_geo", "iterations", "speed":
		default:
			return protocol.JSONObject{}, unsupported("unported usage field " + key)
		}
	}
	// SDK usage's nonnumeric coercions and unknown nested metrics remain outside
	// this finite host profile. Refuse before mutating accumulated usage.
	for _, key := range []string{"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
		if v := part.Get(key); v != nil && string(v) != "null" {
			var n float64
			if json.Unmarshal(v, &n) != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return protocol.JSONObject{}, unsupported("nonnumeric token usage " + key)
			}
		}
	}
	for _, key := range []string{"server_tool_use", "cache_creation"} {
		incoming := object(part.Get(key))
		for _, p := range incoming.OwnProperties() {
			var n float64
			if string(p.Value) != "null" && (json.Unmarshal(p.Value, &n) != nil || math.IsNaN(n) || math.IsInf(n, 0)) {
				return protocol.JSONObject{}, unsupported("nonnumeric nested usage " + key)
			}
		}
	}
	out := previous.Clone()
	for _, key := range []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
		if n := num(part.Get(key)); n > 0 {
			out.Set(key, part.Get(key))
		}
	}
	if v := part.Get("output_tokens"); v != nil && string(v) != "null" {
		out.Set("output_tokens", v)
	}
	for _, key := range []string{"server_tool_use", "cache_creation"} {
		nested := object(out.Get(key))
		incoming := object(part.Get(key))
		for _, property := range nested.OwnProperties() {
			var name string
			_ = json.Unmarshal(property.NameJSON, &name)
			if v := incoming.Get(name); v != nil && string(v) != "null" {
				nested.Set(name, v)
			}
		}
		out.Set(key, raw(nested))
	}
	for _, key := range []string{"iterations", "speed"} {
		if v := part.Get(key); v != nil && string(v) != "null" {
			out.Set(key, v)
		}
	}
	return out, nil
}
func AccumulateUsage(total, message protocol.JSONObject) protocol.JSONObject {
	out := total.Clone()
	for _, key := range []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens"} {
		out.Set(key, raw(num(total.Get(key))+num(message.Get(key))))
	}
	for _, key := range []string{"server_tool_use", "cache_creation"} {
		nested := object(total.Get(key))
		incoming := object(message.Get(key))
		for _, property := range nested.OwnProperties() {
			var name string
			_ = json.Unmarshal(property.NameJSON, &name)
			nested.Set(name, raw(num(property.Value)+num(incoming.Get(name))))
		}
		out.Set(key, raw(nested))
	}
	for _, key := range []string{"service_tier", "inference_geo", "iterations", "speed"} {
		out.Set(key, message.Get(key))
	}
	return out
}

// Accounting admits only the external direct-relay unknown-model profile with
// no override/capability/telemetry/advisor inputs. Source selects third-party
// default Sonnet4.5 ($3/$15),200K context/32K output through real original state.
// Other model/profile resolution is explicitly unsupported, not guessed.
type Calculator struct {
	Model        string
	Total        float64
	Models       protocol.JSONObject
	native       *NativeCalculatorOptions
	UnknownModel bool
}

func NewCalculator(model string, env map[string]string) (*Calculator, error) {
	if model == "" || strings.Contains(strings.ToLower(model), "claude") || strings.ContainsAny(model, "[]:") {
		return nil, unsupported("accounting known/overridden model")
	}
	if env["ANTHROPIC_BASE_URL"] == "" || env["ANTHROPIC_API_KEY"] == "" || (env["CLAUDE_CODE_DISABLE_FAST_MODE"] != "" && env["CLAUDE_CODE_DISABLE_FAST_MODE"] != "1") {
		return nil, unsupported("accounting original external relay auth/fast state")
	}
	return &Calculator{Model: model, Models: protocol.NewJSONObject()}, nil
}
func (a *Calculator) Charge(usage protocol.JSONObject) (float64, error) {
	for _, key := range []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
		n := num(usage.Get(key))
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			return 0, errors.New("invalid actual token usage")
		}
	}
	search := num(object(usage.Get("server_tool_use")).Get("web_search_requests"))
	price := nativeCosts{3, 15, 3.75, 0.3, 0.01}
	window, output := float64(200000), float64(32000)
	a.UnknownModel = true
	if a.native != nil {
		var err error
		price, a.UnknownModel, window, output, err = a.resolveNativePrice(usage)
		if err != nil {
			return 0, err
		}
	}
	// Explicit intermediate float64 conversion preserves JavaScript rounding.
	cost := float64(num(usage.Get("input_tokens"))/1e6) * price.input
	cost = float64(cost + float64(float64(num(usage.Get("output_tokens"))/1e6)*price.output))
	cost = float64(cost + float64(float64(num(usage.Get("cache_read_input_tokens"))/1e6)*price.read))
	cost = float64(cost + float64(float64(num(usage.Get("cache_creation_input_tokens"))/1e6)*price.write))
	cost = float64(cost + float64(search*price.search))
	previous := object(a.Models.Get(a.Model))
	if !previous.IsObject() {
		previous = protocol.NewJSONObject()
		for _, key := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests", "costUSD", "contextWindow", "maxOutputTokens"} {
			previous.Set(key, raw(0))
		}
	}
	keys := [][2]string{{"inputTokens", "input_tokens"}, {"outputTokens", "output_tokens"}, {"cacheReadInputTokens", "cache_read_input_tokens"}, {"cacheCreationInputTokens", "cache_creation_input_tokens"}}
	for _, key := range keys {
		previous.Set(key[0], raw(num(previous.Get(key[0]))+num(usage.Get(key[1]))))
	}
	previous.Set("webSearchRequests", raw(num(previous.Get("webSearchRequests"))+search))
	previous.Set("costUSD", raw(num(previous.Get("costUSD"))+cost))
	previous.Set("contextWindow", raw(window))
	previous.Set("maxOutputTokens", raw(output))
	a.Models.Set(a.Model, raw(previous))
	a.Total += cost
	return cost, nil
}
