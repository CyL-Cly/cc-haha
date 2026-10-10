package requestcontext

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// CountingMetadata and CountingExtraBody retain the same original producers
// as ordinary requests. Token inspection owns no replacement identity.
func CountingMetadata(env map[string]string, device, session string) (protocol.JSONObject, error) {
	encoded, err := metadata(Context{Host: Host{Env: env, DeviceID: device, SessionID: session}})
	if err != nil {
		return protocol.JSONObject{}, err
	}
	var value protocol.JSONObject
	err = value.UnmarshalJSON(encoded)
	return value, err
}

func CountingExtraBody(env map[string]string) protocol.JSONObject {
	return objectEnvironment(env["CLAUDE_CODE_EXTRA_BODY"])
}
