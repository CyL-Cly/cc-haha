package imagegeneration

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

func ImagesURL(base, operation string) (string, error) {
	u, err := weburl.Parse(base)
	if err != nil {
		return "", fmt.Errorf("%s cannot be parsed as a URL.", encoded(base))
	}
	if u.Scheme() != "http" && u.Scheme() != "https" {
		return "", errors.New("Image API Base URL must use HTTP or HTTPS.")
	}
	path := strings.TrimRight(u.Pathname(), "/")
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, "/images/generations") || strings.HasSuffix(lower, "/images/edits") {
		index := strings.LastIndex(path, "/")
		path = path[:index+1] + operation
	} else if strings.HasSuffix(lower, "/v1") {
		path += "/images/" + operation
	} else {
		path += "/v1/images/" + operation
	}
	u.SetPathname(path)
	return u.Href(false), nil
}
func stringValue(record protocol.JSONObject, key string) string {
	raw := record.Get(key)
	if len(raw) == 0 || raw[0] != '"' {
		return ""
	}
	value := record.String(key)
	if protocol.TrimJavaScriptString(value) == "" {
		return ""
	}
	return protocol.TrimJavaScriptString(value)
}
func ParseCompatible(value json.RawMessage) []RawImage {
	var root protocol.JSONObject
	if root.UnmarshalJSON(value) != nil || !root.IsObject() {
		return []RawImage{}
	}
	var items []json.RawMessage
	if json.Unmarshal(root.Get("data"), &items) != nil {
		return []RawImage{}
	}
	images := []RawImage{}
	for _, raw := range items {
		var item protocol.JSONObject
		if item.UnmarshalJSON(raw) != nil || !item.IsObject() {
			continue
		}
		b64, url := stringValue(item, "b64_json"), stringValue(item, "url")
		if b64 == "" && url == "" {
			continue
		}
		images = append(images, RawImage{Base64: b64, URL: url, MIMEType: stringValue(item, "mime_type"), RevisedPrompt: stringValue(item, "revised_prompt")})
	}
	return images
}
func ParseChatGPTStream(stream string) []RawImage {
	images := []RawImage{}
	seen := map[string]bool{}
	for _, line := range strings.Split(stream, "\n") {
		trim := protocol.TrimJavaScriptString(line)
		if !strings.HasPrefix(trim, "data:") {
			continue
		}
		data := protocol.TrimJavaScriptString(trim[5:])
		if data == "" || data == "[DONE]" {
			continue
		}
		var event protocol.JSONObject
		if event.UnmarshalJSON([]byte(data)) != nil || !event.IsObject() {
			continue
		}
		candidates := []json.RawMessage{event.Get("item"), event.Get("output_item")}
		for _, value := range []json.RawMessage{event.Get("response"), event.Get("output")} {
			var object protocol.JSONObject
			if object.UnmarshalJSON(value) == nil && object.IsObject() {
				value = object.Get("output")
			}
			var list []json.RawMessage
			if json.Unmarshal(value, &list) == nil {
				candidates = append(candidates, list...)
			}
		}
		for _, candidate := range candidates {
			var item protocol.JSONObject
			if item.UnmarshalJSON(candidate) != nil || !item.IsObject() || item.String("type") != "image_generation_call" {
				continue
			}
			result := stringValue(item, "result")
			if result == "" || seen[result] {
				continue
			}
			seen[result] = true
			mime := ""
			if format := stringValue(item, "output_format"); format != "" {
				mime = "image/" + format
			}
			images = append(images, RawImage{Base64: result, MIMEType: mime, RevisedPrompt: stringValue(item, "revised_prompt")})
		}
	}
	return images
}
