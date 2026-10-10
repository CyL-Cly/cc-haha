package providerproxy

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// OpenAIUsageToAnthropic is the single original usage mapping shared by
// buffered and streamed Responses. Direct cache fields are exclusive; nested
// OpenAI cache details are inclusive and cannot inflate reported input.
func OpenAIUsageToAnthropic(value json.RawMessage) protocol.JSONObject {
	usage := responseObject(value)
	count := func(value json.RawMessage) *float64 {
		trimmed := strings.TrimSpace(string(value))
		if trimmed == "" || trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9') {
			return nil
		}
		var n float64
		if json.Unmarshal(value, &n) != nil || n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil
		}
		return &n
	}
	first := func(values ...json.RawMessage) *float64 {
		for _, value := range values {
			if n := count(value); n != nil {
				return n
			}
		}
		return nil
	}
	input := first(usage.Get("input_tokens"), usage.Get("prompt_tokens"))
	output := first(usage.Get("output_tokens"), usage.Get("completion_tokens"))
	total := count(usage.Get("total_tokens"))
	if total != nil {
		if input == nil && output != nil {
			n := math.Max(0, *total-*output)
			input = &n
		} else if output == nil && input != nil {
			n := math.Max(0, *total-*input)
			output = &n
		} else if input == nil && output == nil {
			input = total
			n := float64(0)
			output = &n
		}
	}
	i, o := float64(0), float64(0)
	if input != nil {
		i = *input
	}
	if output != nil {
		o = *output
	}
	read, creation := float64(0), float64(0)
	if n := count(usage.Get("cache_read_input_tokens")); n != nil {
		read = *n
	}
	if n := count(usage.Get("cache_creation_input_tokens")); n != nil {
		creation = *n
	}
	if read == 0 && creation == 0 {
		if n := first(responseObject(usage.Get("input_tokens_details")).Get("cached_tokens"), responseObject(usage.Get("prompt_tokens_details")).Get("cached_tokens")); n != nil {
			read = math.Min(*n, i)
		}
		i = math.Max(0, i-read)
	}
	result := responseRecord(responseField("input_tokens", i), responseField("output_tokens", o))
	if read > 0 {
		result.Set("cache_read_input_tokens", responseJSON(read))
	}
	if creation > 0 {
		result.Set("cache_creation_input_tokens", responseJSON(creation))
	}
	return result
}
