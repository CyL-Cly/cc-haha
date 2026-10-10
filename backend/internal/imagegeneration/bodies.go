package imagegeneration

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

type PreparedImage struct {
	DataURL  string `json:"dataUrl"`
	FileName string `json:"fileName"`
	MIMEType string `json:"mimeType"`
	Bytes    []byte `json:"-"`
}
type RawImage struct {
	Base64        string `json:"b64Json,omitempty"`
	URL           string `json:"url,omitempty"`
	MIMEType      string `json:"mimeType,omitempty"`
	RevisedPrompt string `json:"revisedPrompt,omitempty"`
}
type GeneratedImage struct {
	Path          string `json:"path"`
	MIMEType      string `json:"mimeType"`
	RevisedPrompt string `json:"revisedPrompt,omitempty"`
}
type Output struct {
	Type            string           `json:"type"`
	Operation       string           `json:"operation"`
	InputImageCount int              `json:"inputImageCount"`
	ProviderID      string           `json:"providerId"`
	ProviderKind    string           `json:"providerKind"`
	Model           string           `json:"model"`
	Prompt          json.RawMessage  `json:"prompt"`
	Images          []GeneratedImage `json:"images"`
	DurationMS      int64            `json:"durationMs"`
}

func encoded(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return protocol.StringifyValidatedJavaScriptJSON(raw)
}
func object(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func field(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: encoded(value)}
}
func rawField(name string, value json.RawMessage) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: value}
}
func imageSize(input protocol.JSONObject) json.RawMessage {
	if size := input.Get("size"); size != nil && string(size) != "null" {
		return size
	}
	ratio := input.String("aspect_ratio")
	if ratio == "" || ratio == "auto" {
		return nil
	}
	if ratio == "1:1" {
		return encoded("1024x1024")
	}
	for _, portrait := range []string{"9:16", "3:4", "2:3", "1:2", "9:19.5", "9:20"} {
		if ratio == portrait {
			return encoded("1024x1536")
		}
	}
	return encoded("1536x1024")
}
func CompatibleBody(input protocol.JSONObject) protocol.JSONObject {
	return object(rawField("model", input.Get("model")), rawField("prompt", input.Get("prompt")), field("response_format", "b64_json"), rawField("size", imageSize(input)), rawField("quality", input.Get("quality")), rawField("background", input.Get("background")), rawField("output_format", input.Get("output_format")))
}
func ChatGPTBody(input protocol.JSONObject, images []PreparedImage, environment map[string]string) protocol.JSONObject {
	action := "generate"
	if len(images) > 0 {
		action = "edit"
	}
	tool := object(field("type", "image_generation"), field("action", action), rawField("model", input.Get("model")), rawField("size", imageSize(input)), rawField("quality", input.Get("quality")), rawField("background", input.Get("background")), rawField("output_format", input.Get("output_format")))
	content := []protocol.JSONObject{object(field("type", "input_text"), rawField("text", input.Get("prompt")))}
	for _, image := range images {
		content = append(content, object(field("type", "input_image"), field("image_url", image.DataURL)))
	}
	main := protocol.TrimJavaScriptString(environment["ANTHROPIC_MODEL"])
	if main == "" {
		main = providerproxy.OpenAIDefaultMainModel
	}
	return object(field("instructions", ""), field("stream", true), field("reasoning", object(field("effort", "medium"), field("summary", "auto"))), field("parallel_tool_calls", true), field("include", []string{"reasoning.encrypted_content"}), field("model", main), field("store", false), field("tool_choice", object(field("type", "image_generation"))), field("input", []protocol.JSONObject{object(field("type", "message"), field("role", "user"), field("content", content))}), field("tools", []protocol.JSONObject{tool}))
}
func GrokBody(input protocol.JSONObject, images []PreparedImage) protocol.JSONObject {
	ratio := input.Get("aspect_ratio")
	if ratio == nil || string(ratio) == "null" {
		switch input.String("size") {
		case "1024x1024":
			ratio = encoded("1:1")
		case "1024x1536":
			ratio = encoded("2:3")
		case "1536x1024":
			ratio = encoded("3:2")
		default:
			ratio = nil
		}
	}
	result := object(rawField("model", input.Get("model")), rawField("prompt", input.Get("prompt")), rawField("n", input.Get("count")), field("response_format", "b64_json"), rawField("aspect_ratio", ratio), rawField("resolution", input.Get("resolution")))
	if len(images) > 0 {
		sources := make([]protocol.JSONObject, len(images))
		for i, image := range images {
			sources[i] = object(field("type", "image_url"), field("url", image.DataURL))
		}
		if len(sources) == 1 {
			result.SetObject("image", sources[0])
		} else {
			result.Set("images", encoded(sources))
		}
	}
	return result
}
