package toolschema

import (
	"bytes"
	"encoding/json"
)

// OriginalInputJSONSchema supplies autoCompact's original schema-only budget
// declaration. It does not invoke prompts, caches, API strict flags or rendering.
func OriginalInputJSONSchema(definition Definition) (json.RawMessage, error) {
	input := bytes.TrimSpace(definition.InputJSONSchema)
	if len(input) > 0 && !bytes.Equal(input, []byte("null")) {
		object, e := parseObject(input)
		if e != nil || !object.IsObject() {
			return nil, unsupported("original inputJSONSchema must be an object")
		}
		return append(json.RawMessage(nil), definition.InputJSONSchema...), nil
	}
	if definition.InputSchema == nil {
		return nil, unsupported("missing original schema")
	}
	return compileSchema(*definition.InputSchema, true)
}
