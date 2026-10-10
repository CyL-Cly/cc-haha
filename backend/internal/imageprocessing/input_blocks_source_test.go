package imageprocessing

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestSDKInputMediaPassMatchesActualProcessUserInputSource(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Source native fixture dylib is darwin arm64")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	library, _ := filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	codec, err := OpenVips(library)
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	for _, size := range [][2]int{{12, 8}, {9001, 3}, {3, 9001}} {
		im := image.NewNRGBA(image.Rect(0, 0, size[0], size[1]))
		for y := 0; y < size[1]; y++ {
			for x := 0; x < size[0]; x++ {
				im.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), 99, 137})
			}
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, im); err != nil {
			t.Fatal(err)
		}
		data := base64.StdEncoding.EncodeToString(encoded.Bytes())
		block := map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/jpeg", "data": data, "future": "drop-source"}, "cache_control": map[string]any{"type": "ephemeral"}, "future": "drop-block"}
		for _, parts := range [][]any{{block}, {map[string]any{"type": "text", "text": "last text"}, block}, {block, map[string]any{"type": "text", "text": "last text"}}} {
			rows = append(rows, map[string]any{"content": parts, "uuid": "source-fixed", "mode": "default"})
		}
		delete(block["source"].(map[string]any), "media_type")
		block["source"].(map[string]any)["mediaType"] = "image/webp"
		rows = append(rows, map[string]any{"content": []any{block}, "uuid": "source-fixed", "mode": "default", "meta": true})
	}
	for _, raw := range []string{
		`[]`, `"normal input"`,
		`[{"type":"image","source":{"type":"url","url":"https://private.invalid/not-fetched","future":"keep"},"cache_control":{"type":"ephemeral"},"future":"keep"}]`,
		`[{"type":"document","title":"Private title","context":"Source context","citations":{"enabled":true},"source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0xLjQ="},"future":{"text":"\\ud800"}}]`,
		`[{"type":"document","source":{"type":"text","media_type":"text/plain","data":"private text"}}]`,
		`[{"type":"document","source":{"type":"url","url":"https://private.invalid/not-fetched"}}]`,
		`[{"type":"document","source":{"type":"content","content":[{"type":"text","text":"content source"},{"type":"image","source":{"type":"url","url":"https://private.invalid/nested"}}]}}]`,
		`[{"type":"document","source":{"type":"content","content":"raw content source"}}]`,
		`[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"!!!"}}]`,
		`[{"type":"image","source":{"type":"base64","data":"eA"}}]`,
	} {
		var content any
		if err := json.Unmarshal([]byte(raw), &content); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, map[string]any{"content": content, "uuid": "source-fixed", "mode": "default"})
	}
	base64rows := []string{"", "Y Q==ignored", "_w--", "eA", "e", "YĀQ==", "YŁQ==", "Y😀Q==", "😀YQ", "YQ=AA", "aGVsbG8\n!"}
	home := t.TempDir()
	_ = os.Mkdir(filepath.Join(home, ".claude"), 0700)
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-input-media.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_INPUT_MEDIA_FIXTURE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows, "base64": base64rows})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Content     json.RawMessage
			Metadata    []json.RawMessage
			Error       *string
			ShouldQuery bool
			FirstMeta   bool
		}
		Base64 []string
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(output))
	}
	for i, row := range rows {
		raw, _ := json.Marshal(row["content"])
		actual, texts, err := InputBlocks(context.Background(), codec, raw)
		want := source.Rows[i]
		if want.Error != nil {
			if err == nil || err.Error() != *want.Error {
				t.Errorf("row%d error Go%v Source%s", i, err, *want.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("row%d %v", i, err)
			continue
		}
		var got, wanted any
		_ = json.Unmarshal(actual, &got)
		_ = json.Unmarshal(want.Content, &wanted)
		if !reflect.DeepEqual(got, wanted) || !reflect.DeepEqual(texts, want.Metadata) || !want.ShouldQuery {
			t.Errorf("row%d Go%v meta%s Source%v meta%s", i, got, texts, wanted, want.Metadata)
		}
	}
	for i, text := range base64rows {
		raw, _ := json.Marshal(text)
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		got := base64.StdEncoding.EncodeToString(DecodeInputBase64(units))
		if got != source.Base64[i] {
			t.Errorf("base64%d Go%s Source%s", i, got, source.Base64[i])
		}
	}
}
