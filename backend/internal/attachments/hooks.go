package attachments

import (
	"bytes"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func hookAdditionalContent(raw json.RawMessage) (json.RawMessage, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, false, malformed("undefined is not an object (evaluating 'attachment.content.length')")
	}
	if string(raw) == "null" {
		return nil, false, malformed("null is not an object (evaluating 'attachment.content.length')")
	}
	if raw[0] != '[' {
		if string(raw) == `""` {
			return nil, true, nil
		}
		if raw[0] == '{' {
			v, _ := object(raw)
			if string(v.Get("length")) == "0" || string(v.Get("length")) == "-0" {
				return nil, true, nil
			}
		}
		return nil, false, malformed("attachment.content.join is not a function. (In 'attachment.content.join(`\n`)', 'attachment.content.join' is undefined)")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, false, err
	}
	if len(values) == 0 {
		return nil, true, nil
	}
	parts := []json.RawMessage{}
	for i, value := range values {
		if i > 0 {
			parts = append(parts, quoted("\n"))
		}
		if string(bytes.TrimSpace(value)) == "null" {
			continue
		}
		text, err := protocol.ExactJavaScriptStringJSON(value)
		if err != nil {
			var failure *protocol.ExactJSONStringTypeError
			if e, ok := err.(*protocol.ExactJSONStringTypeError); ok {
				failure = e
			}
			if failure != nil {
				return nil, false, malformed(failure.Message)
			}
			return nil, false, err
		}
		parts = append(parts, text)
	}
	text, err := concatenate(parts...)
	return text, false, err
}
