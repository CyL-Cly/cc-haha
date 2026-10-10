package sdkhost

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
)

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
func zeroUsage() protocol.JSONObject {
	return protocol.NewJSONObject(
		protocol.JSONField{Name: "input_tokens", Value: raw(0)}, protocol.JSONField{Name: "cache_creation_input_tokens", Value: raw(0)},
		protocol.JSONField{Name: "cache_read_input_tokens", Value: raw(0)}, protocol.JSONField{Name: "output_tokens", Value: raw(0)},
		protocol.JSONField{Name: "server_tool_use", Value: json.RawMessage(`{"web_search_requests":0,"web_fetch_requests":0}`)},
		protocol.JSONField{Name: "service_tier", Value: quote("standard")},
		protocol.JSONField{Name: "cache_creation", Value: json.RawMessage(`{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0}`)},
		protocol.JSONField{Name: "inference_geo", Value: quote("")}, protocol.JSONField{Name: "iterations", Value: raw([]any{})}, protocol.JSONField{Name: "speed", Value: quote("standard")})
}

// updateUsage retains cumulative stream totals and the Source's >0 input guard.
func updateUsage(previous, part protocol.JSONObject) (protocol.JSONObject, error) {
	if !part.IsObject() {
		return previous.Clone(), nil
	}
	for _, property := range part.OwnProperties() {
		var key string
		_ = json.Unmarshal(property.NameJSON, &key)
		switch key {
		case "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "server_tool_use", "cache_creation", "service_tier", "inference_geo", "iterations", "speed":
		default:
			return protocol.JSONObject{}, cap("unported usage field " + key)
		}
	}
	// SDK usage's nonnumeric coercions and unknown nested metrics remain outside
	// this finite host profile. Refuse before mutating accumulated usage.
	for _, key := range []string{"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
		if v := part.Get(key); v != nil && string(v) != "null" {
			var n float64
			if json.Unmarshal(v, &n) != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return protocol.JSONObject{}, cap("nonnumeric token usage " + key)
			}
		}
	}
	for _, key := range []string{"server_tool_use", "cache_creation"} {
		incoming := object(part.Get(key))
		for _, p := range incoming.OwnProperties() {
			var n float64
			if string(p.Value) != "null" && (json.Unmarshal(p.Value, &n) != nil || math.IsNaN(n) || math.IsInf(n, 0)) {
				return protocol.JSONObject{}, cap("nonnumeric nested usage " + key)
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
func accumulateUsage(total, message protocol.JSONObject) protocol.JSONObject {
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

// Accounting is a historical component adapter and transient result view.
// Actual SDK production never retains or charges it; its Controller session
// is the unique mutable cost authority.
type Accounting struct {
	model  string
	total  float64
	models protocol.JSONObject
}

func newAccounting(model string, env map[string]string) (*Accounting, error) {
	value, err := modelaccounting.NewCalculator(model, env)
	if err != nil {
		var capability *modelaccounting.CapabilityError
		if errors.As(err, &capability) {
			return nil, cap(capability.Boundary)
		}
		return nil, err
	}
	return &Accounting{model: value.Model, total: value.Total, models: value.Models}, nil
}
func (a *Accounting) charge(usage protocol.JSONObject) (float64, error) {
	value := modelaccounting.Calculator{Model: a.model, Total: a.total, Models: a.models}
	cost, err := value.Charge(usage)
	if err == nil {
		a.total, a.models = value.Total, value.Models
	}
	return cost, err
}

// Validation retains refusal before stdout/network for unsupported pricing.
// The checked calculator is discarded without charge or retained mutable state.
func validateOriginalAccountingProfile(model string, env map[string]string) error {
	_, err := modelaccounting.NewCalculator(model, env)
	var capability *modelaccounting.CapabilityError
	if errors.As(err, &capability) {
		return cap(capability.Boundary)
	}
	return err
}
