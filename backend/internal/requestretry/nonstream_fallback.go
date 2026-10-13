package requestretry

import (
	"encoding/json"
	"math"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestmapping"
)

func prepareNativeFallback(result *requestcontext.Result, deferCCH bool) (*provider.MessagesRequest, error) {
	raw, err := result.Params.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var params protocol.JSONObject
	if err = params.UnmarshalJSON(raw); err != nil {
		return nil, err
	}
	params.Delete("stream")
	var maximum float64
	if err = json.Unmarshal(params.Get("max_tokens"), &maximum); err != nil {
		return nil, err
	}
	maximum = math.Min(maximum, 64000)
	raw, err = json.Marshal(maximum)
	if err != nil {
		return nil, err
	}
	params.Set("max_tokens", raw)
	var thinking protocol.JSONObject
	_ = thinking.UnmarshalJSON(params.Get("thinking"))
	if thinking.String("type") == "enabled" {
		var budget float64
		if json.Unmarshal(thinking.Get("budget_tokens"), &budget) == nil && budget != 0 {
			raw, err = json.Marshal(math.Min(budget, maximum-1))
			if err != nil {
				return nil, err
			}
			thinking.Set("budget_tokens", raw)
			raw, err = thinking.MarshalJSON()
			if err != nil {
				return nil, err
			}
			params.Set("thinking", raw)
		}
	}
	raw, err = params.MarshalJSON()
	if err != nil {
		return nil, err
	}
	value, err := requestmapping.ParseValue(raw)
	if err != nil {
		return nil, err
	}
	prepared, err := provider.PrepareBetaMessages(value, provider.BetaMessageOptions{DeferCCH: deferCCH})
	if err != nil {
		return nil, err
	}
	// Output-budget headers describe the original requested limit, before cap.
	prepared.Headers = result.PreparedRequest.Headers.Clone()
	prepared.RemovedHeaders = append([]string(nil), result.PreparedRequest.RemovedHeaders...)
	return prepared, nil
}
