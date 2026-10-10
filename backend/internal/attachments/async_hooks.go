package attachments

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func projectAsyncHook(value protocol.JSONObject, factory *messages.Factory) ([]protocol.JSONObject, error) {
	if factory == nil {
		return nil, errors.New("asynchronous Hook response requires owning message factory")
	}
	raw := bytes.TrimSpace(value.Get("response"))
	if len(raw) == 0 {
		return nil, malformed("undefined is not an object (evaluating 'response.systemMessage')")
	}
	if bytes.Equal(raw, []byte("null")) {
		return nil, malformed("null is not an object (evaluating 'response.systemMessage')")
	}
	response, err := object(raw)
	if err != nil || !response.IsObject() {
		return []protocol.JSONObject{}, nil
	}
	contents := []json.RawMessage{}
	if content := response.Get("systemMessage"); truthy(content) {
		contents = append(contents, content)
	}
	if specific, ok := response.Object("hookSpecificOutput"); ok {
		if content := specific.Get("additionalContext"); truthy(content) {
			contents = append(contents, content)
		}
	}
	// Source creates both users before wrapping either; preserve factory calls
	// even for malformed array text whose wrapper subsequently throws.
	out := []protocol.JSONObject{}
	for _, content := range contents {
		out = append(out, messages.OrderedUser(factory.CreateUser(storage.Object{"content": content, "isMeta": json.RawMessage("true")})))
	}
	for i := range out {
		api, _ := out[i].Object("message")
		content := bytes.TrimSpace(api.Get("content"))
		if len(content) > 0 && content[0] == '"' {
			wrapped, err := concatenate(quoted("<system-reminder>\n"), content, quoted("\n</system-reminder>"))
			if err != nil {
				return nil, err
			}
			api.Set("content", wrapped)
		} else if len(content) > 0 && content[0] == '[' {
			var blocks []json.RawMessage
			if err = json.Unmarshal(content, &blocks); err != nil {
				return nil, err
			}
			for j, raw := range blocks {
				if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
					return nil, malformed("null is not an object (evaluating 'block.type')")
				}
				block, err := object(raw)
				if err != nil || block.String("type") != "text" {
					continue
				}
				text, err := protocol.JavaScriptStringJSON(block.Get("text"))
				if err != nil {
					return nil, err
				}
				wrapped, err := concatenate(quoted("<system-reminder>\n"), text, quoted("\n</system-reminder>"))
				if err != nil {
					return nil, err
				}
				block.Set("text", wrapped)
				blocks[j], _ = block.MarshalJSON()
			}
			encoded, _ := json.Marshal(blocks)
			api.Set("content", encoded)
		}
		encoded, _ := api.MarshalJSON()
		out[i].Set("message", encoded)
	}
	return out, nil
}
