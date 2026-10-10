package preflightcore

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// CurrentContextUsage is Source getCurrentUsage over the same original usage
// classifier as ASK. Placeholder/synthetic and invalid usage cannot anchor it.
func CurrentContextUsage(original []protocol.JSONObject) (*protocol.JSONObject, error) {
	history, err := originalHistory(original)
	if err != nil {
		return nil, err
	}
	for i := len(history) - 1; i >= 0; i-- {
		raw := usage(history[i])
		if !truthy(raw) {
			continue
		}
		ok, err := usable(raw)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		value := object(raw)
		result := protocol.NewJSONObject()
		for _, key := range []string{"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
			count, _ := safeNumber(value.Get(key))
			encoded, _ := json.Marshal(count)
			result.Set(key, encoded)
		}
		return &result, nil
	}
	return nil, nil
}
