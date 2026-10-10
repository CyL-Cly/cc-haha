package requestcontext

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Source passes SDK document sources through processTextPrompt and message
// normalization. Validation here covers all SDK source variants without
// rebuilding their title/context/citations/cache-control or future fields.
func ValidateDocumentBlock(block protocol.JSONObject) error {
	source, err := parseObject(block.Get("source"))
	if err != nil || !source.IsObject() {
		return unsupported("document source")
	}
	stringField := func(name string) error {
		_, err := protocol.DecodeJavaScriptJSONString(source.Get(name))
		if err != nil {
			return unsupported("document " + name)
		}
		return nil
	}
	switch source.String("type") {
	case "base64", "text":
		return stringField("data")
	case "url":
		return stringField("url")
	case "content":
		value := source.Get("content")
		if _, err := protocol.DecodeJavaScriptJSONString(value); err == nil {
			return nil
		}
		var parts []protocol.JSONObject
		if json.Unmarshal(value, &parts) != nil || parts == nil {
			return unsupported("document content source")
		}
		for _, part := range parts {
			switch part.String("type") {
			case "text":
				if _, err := protocol.DecodeJavaScriptJSONString(part.Get("text")); err != nil {
					return unsupported("document text")
				}
			case "image":
				if err := ValidateProcessedImageBlock(part); err != nil {
					return err
				}
			default:
				return unsupported("document content block " + part.String("type"))
			}
		}
		return nil
	default:
		return unsupported("document source " + source.String("type"))
	}
}
