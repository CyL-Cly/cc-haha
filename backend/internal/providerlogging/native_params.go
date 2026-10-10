package providerlogging

import (
	"bytes"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestmapping"
)

func paramsRaw(value *requestmapping.Value) json.RawMessage {
	if value == nil {
		return nil
	}
	raw, _ := value.MarshalJSON()
	return append(json.RawMessage(nil), raw...)
}

// FromParams captures Source's pre-client query log scalars from the actual
// paramsFromContext result. Original options still supply model and temperature:
// extra-body overrides and removal of [1m] affect the wire, not those log fields.
func FromParams(input requestcontext.Context, result *requestcontext.Result, fastMode *bool) (QueryInput, error) {
	if input.Host.Profile != requestcontext.NativeDirectRelayProfile || result == nil || result.Params == nil || result.PreparedRequest == nil {
		return QueryInput{}, &CapabilityError{"native original prepared params"}
	}
	length, array := result.Params.Property("messages").ArrayLength()
	if !array {
		return QueryInput{}, &CapabilityError{"non-array prepared messages length"}
	}
	output := QueryInput{Model: input.Options.Model, QuerySource: input.Options.QuerySource, MessagesLength: length, Temperature: 1, Environment: map[string]string{}, PreviousRequestID: append(json.RawMessage(nil), result.PreviousRequestID...)}
	if input.Options.TemperatureOverride != nil {
		output.Temperature = *input.Options.TemperatureOverride
	}
	if fastMode != nil {
		output.FastMode = *fastMode
	}
	for name, value := range input.Host.Env {
		output.Environment[name] = value
	}
	output.ThinkingTypeValue = paramsRaw(result.Params.Property("thinking").Property("type"))
	if output.ThinkingTypeValue == nil || bytes.Equal(output.ThinkingTypeValue, []byte("null")) {
		output.ThinkingTypeValue = data("disabled")
	}
	output.EffortValue = paramsRaw(result.Params.Property("output_config").Property("effort"))
	if raw := input.Options.QueryTracking; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var tracking protocol.JSONObject
		if err := tracking.UnmarshalJSON(raw); err != nil || !tracking.IsObject() {
			return QueryInput{}, &CapabilityError{"native original query tracking object"}
		}
		output.QueryTracking = append(json.RawMessage(nil), raw...)
	}
	betas := result.Params.Property("betas")
	if betas != nil && !bytes.Equal(paramsRaw(betas), []byte("null")) {
		count, array := betas.ArrayLength()
		if !array {
			return QueryInput{}, &CapabilityError{"non-array prepared beta join"}
		}
		if count > 0 {
			joined := []uint16{}
			for index := 0; index < count; index++ {
				if index > 0 {
					joined = append(joined, ',')
				}
				raw := paramsRaw(betas.Element(index))
				// Array.join leaves nullish elements empty; other JSON values use
				// the existing exact String conversion, including nested arrays.
				if raw == nil || bytes.Equal(raw, []byte("null")) {
					continue
				}
				quoted, err := protocol.ExactJavaScriptStringJSON(raw)
				if err != nil {
					return QueryInput{}, err
				}
				units, err := protocol.DecodeJavaScriptJSONString(quoted)
				if err != nil {
					return QueryInput{}, err
				}
				joined = append(joined, units...)
			}
			output.BetasJoined = protocol.QuoteJavaScriptUTF16(joined)
		}
	}
	return output, nil
}
