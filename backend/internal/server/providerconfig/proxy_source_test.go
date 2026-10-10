package providerconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestSavedProviderProxySelectionAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	service, config := fixture(t)
	home := filepath.Dir(config)
	rows := []map[string]any{}
	var presets []storage.Object
	_ = json.Unmarshal(presetJSON, &presets)
	for _, preset := range append(presets, storage.Object{"id": encode("unknown-preset")}) {
		for _, key := range []string{"", "fixture-key"} {
			for _, nested := range []any{nil, false, true} {
				p := saved("fixture")
				p["presetId"] = preset["id"]
				p["apiKey"] = encode(key)
				p["apiFormat"] = encode("anthropic")
				p["requestCompatibility"] = encode(map[string]any{"maxOutputTokens": 4096, "maxOutputTokenField": "max_completion_tokens", "future": true})
				if nested != nil {
					p["supportsNestedToolResultMedia"] = encode(nested)
				}
				index := storage.Object{"schemaVersion": encode(6), "activeId": encode("fixture"), "providers": encode([]storage.Object{p})}
				for _, scoped := range []bool{false, true} {
					row := map[string]any{"index": index}
					if scoped {
						row["id"] = "fixture"
					}
					rows = append(rows, row)
				}
			}
		}
	}
	for _, active := range []any{nil, "", "missing", "claude-official", "openai-official", "grok-official"} {
		for _, id := range []string{"", "missing", "claude-official", "openai-official", "grok-official"} {
			index := storage.Object{"schemaVersion": encode(6), "activeId": encode(active), "providers": encode([]storage.Object{})}
			row := map[string]any{"index": index}
			if id != "" {
				row["id"] = id
			}
			rows = append(rows, row)
		}
	}
	fixturePath, _ := filepath.Abs("../../../../scripts/quality-gate/go-parity/native-provider-proxy-selection.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	cmd.Stdin = bytes.NewReader(encode(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual ProviderService %v %s", err, stderr.String())
	}
	var expected []map[string]any
	if json.Unmarshal(output, &expected) != nil || len(expected) != len(rows) {
		t.Fatal(string(output), stderr.String())
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			seed(t, service.indexPath(), string(encode(row["index"])))
			id, _ := row["id"].(string)
			value, err := service.ProviderForProxy(ctx, id)
			actual := map[string]any{}
			if err != nil {
				failure, ok := err.(*settings.APIError)
				if !ok {
					t.Fatal(err)
				}
				actual["error"] = map[string]any{"statusCode": float64(failure.StatusCode), "code": failure.Code, "message": failure.Message}
			} else {
				var v any
				_ = json.Unmarshal(encode(value), &v)
				actual["value"] = v
			}
			if !reflect.DeepEqual(actual, expected[i]) {
				t.Fatalf("Source%s Go%s", encode(expected[i]), encode(actual))
			}
		})
	}
}
