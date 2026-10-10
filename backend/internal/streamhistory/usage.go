package streamhistory

import (
	"bytes"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// EMPTY_USAGE and updateUsage are the query runtime's defaults; these differ
// from the synthetic assistant factory's null-valued default usage.
const emptyUsage = `{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"standard","cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},"inference_geo":"","iterations":[],"speed":"standard"}`

func object(raw json.RawMessage) protocol.JSONObject {
	var value protocol.JSONObject
	_ = json.Unmarshal(raw, &value)
	return value
}
func nullish(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
func coalesce(a, b json.RawMessage) json.RawMessage {
	if nullish(a) {
		return b
	}
	return a
}
func updateUsage(previous *Usage, raw json.RawMessage) (*Usage, error) {
	old := object(previous.Raw)
	if nullish(raw) {
		return &Usage{Raw: append(json.RawMessage(nil), previous.Raw...)}, nil
	}
	part := object(raw)
	result := jsonObject()
	for _, name := range []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
		value := part.Get(name)
		keep, err := positive(value)
		if err != nil {
			return nil, err
		}
		if !keep {
			value = old.Get(name)
		}
		result.Set(name, value)
	}
	result.Set("output_tokens", coalesce(part.Get("output_tokens"), old.Get("output_tokens")))
	oldServer, partServer := object(old.Get("server_tool_use")), object(part.Get("server_tool_use"))
	server := jsonObject()
	for _, name := range []string{"web_search_requests", "web_fetch_requests"} {
		server.Set(name, coalesce(partServer.Get(name), oldServer.Get(name)))
	}
	result.Set("server_tool_use", compact(server))
	result.Set("service_tier", old.Get("service_tier"))
	oldCache, partCache := object(old.Get("cache_creation")), object(part.Get("cache_creation"))
	cache := jsonObject()
	for _, name := range []string{"ephemeral_1h_input_tokens", "ephemeral_5m_input_tokens"} {
		cache.Set(name, coalesce(partCache.Get(name), oldCache.Get(name)))
	}
	result.Set("cache_creation", compact(cache))
	result.Set("inference_geo", old.Get("inference_geo"))
	result.Set("iterations", coalesce(part.Get("iterations"), old.Get("iterations")))
	result.Set("speed", coalesce(part.Get("speed"), old.Get("speed")))
	return &Usage{Raw: compact(result)}, nil
}
