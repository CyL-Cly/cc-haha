package requestcontext

import "encoding/json"

// NativeSDKAppliedEffort exposes the same effort policy used by native ASK.
// SDK get_settings uses the AppState value without Agent request overrides,
// and projects a numeric or absent result as null in its string-level schema.
func NativeSDKAppliedEffort(model string, effort json.RawMessage, environment map[string]string) json.RawMessage {
	input := Context{Options: Options{Model: model, EffortValue: effort}, Host: Host{Profile: NativeDirectRelayProfile, Env: environment}}
	if !nativeSupportsEffort(input) {
		return json.RawMessage("null")
	}
	value := nativeEffort(input)
	var text string
	if len(value) == 0 || json.Unmarshal(value, &text) != nil {
		return json.RawMessage("null")
	}
	return append(json.RawMessage(nil), value...)
}
