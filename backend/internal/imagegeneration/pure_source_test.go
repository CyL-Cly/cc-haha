package imagegeneration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func inputObject(value any) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(encoded(value))
	return o
}
func TestImagePureContractsAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{}
	for _, kind := range []string{"", "openai_oauth", "grok_oauth", "openai_images", "unknown"} {
		for _, model := range []string{"", " gpt-image-2 "} {
			for _, auth := range []bool{false, true} {
				env := map[string]string{"CC_HAHA_IMAGE_PROVIDER_KIND": kind, "CC_HAHA_IMAGE_PROVIDER_ID": " p ", "CC_HAHA_IMAGE_MODEL": model}
				if auth {
					env["CC_HAHA_IMAGE_BASE_URL"] = " https://fixture.invalid/v1 "
					env["CC_HAHA_IMAGE_API_KEY"] = " fake "
				}
				rows = append(rows, map[string]any{"kind": "config", "env": env})
			}
		}
	}
	for _, ratio := range []any{nil, "auto", "1:1", "9:16", "16:9", "3:4", "19.5:9", "9:20"} {
		for _, size := range []any{nil, "auto", "1024x1536"} {
			for _, kind := range []string{"chat", "grok", "compatible", "smart"} {
				input := map[string]any{"model": "private-image-model", "prompt": json.RawMessage(`"A <poster> \ud800"`), "count": 2}
				if ratio != nil {
					input["aspect_ratio"] = ratio
				}
				if size != nil {
					input["size"] = size
				}
				rows = append(rows, map[string]any{"kind": kind, "input": input})
			}
		}
	}
	for _, main := range []string{"", "\ufeff gpt-6-sol \u00a0"} {
		rows = append(rows, map[string]any{"kind": "chat", "model": main, "input": map[string]any{"model": "gpt-image-2", "prompt": "edit", "count": 1, "output_format": "png"}, "images": []map[string]any{{"dataUrl": "data:image/png;base64,cHJpdmF0ZQ==", "fileName": "a.png", "mimeType": "image/png"}, {"dataUrl": "data:image/jpeg;base64,cHJpdmF0ZTI=", "fileName": "b.jpg", "mimeType": "image/jpeg"}}})
	}
	for _, base := range []string{"https://fixture.invalid", "https://fixture.invalid/v1/", "https://fixture.invalid/images/generations?x=1#part", "https://fixture.invalid/V1/images/EDITS/", "ftp://fixture.invalid", "bad", "https://u:p@fixture.invalid/custom"} {
		for _, op := range []string{"generations", "edits"} {
			rows = append(rows, map[string]any{"kind": "url", "base": base, "operation": op})
		}
	}
	for _, value := range []any{nil, false, []any{}, map[string]any{}, map[string]any{"data": []any{nil, false, map[string]any{"b64_json": "  fake-base64  ", "mime_type": " image/png ", "revised_prompt": " revised "}, map[string]any{"url": " https://fixture.invalid/a.png "}, map[string]any{"b64_json": 17}}}} {
		rows = append(rows, map[string]any{"kind": "response", "value": value})
	}
	rows = append(rows, map[string]any{"kind": "stream", "stream": "data: {\"item\":{\"type\":\"image_generation_call\",\"result\":\"image1\",\"output_format\":\"png\"}}\r\ndata: {\"response\":{\"output\":[{\"type\":\"image_generation_call\",\"result\":\"image1\"},{\"type\":\"image_generation_call\",\"result\":\" image2 \",\"revised_prompt\":\" revised \"}]}}\ndata: broken\ndata: [DONE]"})
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-imagegen-pure.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_IMAGEGEN_PURE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(encoded(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Image Source: %v %s", err, stderr.String())
	}
	var expected []json.RawMessage
	if json.Unmarshal(data, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source shape: %s", data)
	}
	for i, row := range rows {
		var value any
		var e error
		input := inputObject(row["input"])
		images := []PreparedImage{}
		if list, p := row["images"].([]map[string]any); p {
			for _, image := range list {
				images = append(images, PreparedImage{DataURL: image["dataUrl"].(string), FileName: image["fileName"].(string), MIMEType: image["mimeType"].(string)})
			}
		}
		switch row["kind"] {
		case "config":
			value = RuntimeConfig(row["env"].(map[string]string))
		case "chat":
			env := map[string]string{}
			if model, p := row["model"].(string); p {
				env["ANTHROPIC_MODEL"] = model
			}
			value = ChatGPTBody(input, images, env)
		case "grok":
			value = GrokBody(input, images)
		case "compatible":
			value = CompatibleBody(input)
		case "smart":
			value, e = APISmartFields(input, images)
		case "url":
			value, e = ImagesURL(row["base"].(string), row["operation"].(string))
		case "response":
			value = ParseCompatible(encoded(row["value"]))
		case "stream":
			value = ParseChatGPTStream(row["stream"].(string))
		}
		result := map[string]any{"value": value}
		if e != nil {
			result = map[string]any{"error": e.Error()}
		}
		var got, want any
		_ = json.Unmarshal(encoded(result), &got)
		_ = json.Unmarshal(expected[i], &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("row%d %v Go%s\nSource%s", i, row["kind"], encoded(result), expected[i])
		}
	}
}
