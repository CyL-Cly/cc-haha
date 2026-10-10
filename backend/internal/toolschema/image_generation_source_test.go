package toolschema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestImageMetadataAndInputAgainstActualToolSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{}
	for _, name := range []string{"ImageGen", "ImageEdit"} {
		for _, input := range []any{map[string]any{}, map[string]any{"prompt": ""}, map[string]any{"prompt": "image"}, map[string]any{"prompt": "image", "count": 1}, map[string]any{"prompt": "image", "count": 0}, map[string]any{"prompt": "image", "count": 5}, map[string]any{"prompt": "image", "count": 1.5}, map[string]any{"prompt": "image", "size": "unknown"}, map[string]any{"prompt": "image", "resolution": "2k", "quality": "high", "background": "transparent", "output_format": "webp", "aspect_ratio": "9:19.5"}, map[string]any{"prompt": "image", "referenced_image_paths": []string{}}, map[string]any{"prompt": "image", "referenced_image_paths": []string{"/private/provided.png"}}, map[string]any{"prompt": "image", "referenced_image_paths": []string{""}}, map[string]any{"prompt": "image", "referenced_image_paths": []string{"a", "b", "c", "d"}}, map[string]any{"prompt": "image", "model": "invented"}} {
			rows = append(rows, map[string]any{"name": name, "input": input})
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-imagegen-tool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_IMAGEGEN_TOOL_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(rows)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("Image tool Source: %v %s", err, stderr.String())
	}
	var expected []json.RawMessage
	if json.Unmarshal(data, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source tool shape: %s", data)
	}
	for i, row := range rows {
		def, err := NativeImageDefinition(row["name"].(string))
		if err != nil {
			t.Fatal(err)
		}
		schema, err := compileSchema(*def.InputSchema, true)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(row["input"])
		var input protocol.JSONObject
		_ = input.UnmarshalJSON(raw)
		value, err := ParseInput(def, input)
		parsed := map[string]any{"value": value}
		if err != nil {
			var v *ValidationError
			if !errors.As(err, &v) {
				t.Fatal(err)
			}
			parsed = map[string]any{"issues": v.Issues}
		}
		result := map[string]any{"description": def.Description, "prompt": def.Prompt, "searchHint": def.SearchHint, "strict": def.Strict, "shouldDefer": def.ShouldDefer, "schema": schema, "parsed": parsed}
		actual, _ := json.Marshal(result)
		var got, want any
		_ = json.Unmarshal(actual, &got)
		_ = json.Unmarshal(expected[i], &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("row%d Go%s\nSource%s", i, actual, expected[i])
		}
	}
}
