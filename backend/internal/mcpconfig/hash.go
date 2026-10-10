package mcpconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Hash matches the original reload producer: provenance scope is excluded,
// object keys use JavaScript's UTF-16 sort and array ordering is retained.
func Hash(config protocol.JSONObject) string {
	config = config.Clone()
	config.Delete("scope")
	var stable func(json.RawMessage) json.RawMessage
	stable = func(value json.RawMessage) json.RawMessage {
		object := object(value)
		if object.IsObject() {
			properties := object.OwnProperties()
			sort.SliceStable(properties, func(i, j int) bool {
				a, _ := protocol.DecodeJavaScriptJSONString(properties[i].NameJSON)
				b, _ := protocol.DecodeJavaScriptJSONString(properties[j].NameJSON)
				for at := 0; at < len(a) && at < len(b); at++ {
					if a[at] != b[at] {
						return a[at] < b[at]
					}
				}
				return len(a) < len(b)
			})
			out := protocol.NewJSONObject()
			for _, p := range properties {
				if name(p) != "__proto__" {
					_ = out.SetPropertyJSON(p.NameJSON, stable(p.Value))
				}
			}
			return encode(out)
		}
		if len(value) > 0 && value[0] == '[' {
			values := list(value)
			for i, v := range values {
				values[i] = stable(v)
			}
			return encode(values)
		}
		return value
	}
	sum := sha256.Sum256(protocol.StringifyValidatedJavaScriptJSON(stable(encode(config))))
	return hex.EncodeToString(sum[:])[:16]
}
