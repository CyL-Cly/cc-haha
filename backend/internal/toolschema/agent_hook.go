package toolschema

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// ParseAgentHookOutput is the original hookResponseSchema Zod object. The
// normal registered-schema parser remains restricted to its existing profile.
func ParseAgentHookOutput(input protocol.JSONObject) (protocol.JSONObject, error) {
	schema := &Schema{Kind: "object", UnknownKeys: "strip", Fields: []Field{{Name: "ok", Schema: Schema{Kind: "boolean"}}, {Name: "reason", Schema: Schema{Kind: "string", Optional: true}}}}
	return parseNativeInput(Definition{InputSchema: schema}, input)
}
