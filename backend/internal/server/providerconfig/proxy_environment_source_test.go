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

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestProxyManagedEnvironmentActivationAndScopedRuntimeAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	service, config := fixture(t)
	home := filepath.Dir(config)
	rows := []map[string]any{}
	var presets []storage.Object
	_ = json.Unmarshal(presetJSON, &presets)
	for _, preset := range append(presets, storage.Object{"id": encode("custom")}) {
		for _, format := range []string{"anthropic", "openai_chat", "openai_responses"} {
			for _, nested := range []bool{false, true} {
				p := saved("fixture")
				p["presetId"] = preset["id"]
				p["apiFormat"] = encode(format)
				p["supportsNestedToolResultMedia"] = encode(nested)
				p["models"] = encode(map[string]string{"main": " kimi-k3 ", "haiku": "glm-5.3", "sonnet": "glm-5.2", "opus": "deepseek-v4-flash", "fable": "generic-model"})
				p["model1mSupport"] = encode(map[string]bool{"main": true, "haiku": false, "sonnet": false, "opus": true})
				p["modelContextWindows"] = encode(map[string]int{"owned-model": 65536})
				p["requestCompatibility"] = encode(map[string]any{"maxOutputTokens": 4096})
				p["toolSearchEnabled"] = encode(true)
				rows = append(rows, map[string]any{"port": 5178, "index": storage.Object{"schemaVersion": encode(6), "activeId": encode("fixture"), "providers": encode([]storage.Object{p})}})
			}
		}
	}
	fixturePath, _ := filepath.Abs("../../../../scripts/quality-gate/go-parity/native-provider-proxy-environment.fixture.ts")
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
		t.Fatalf("actual provider runtime %v %s", err, stderr.String())
	}
	var expected []any
	if json.Unmarshal(output, &expected) != nil || len(expected) != len(rows) {
		t.Fatal(string(output), stderr.String())
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			seed(t, service.indexPath(), string(encode(row["index"])))
			seed(t, filepath.Join(config, "cc-haha", "settings.json"), `{"future":true,"env":{"RETAIN":"owned","ANTHROPIC_API_KEY":"old","ANTHROPIC_AUTH_TOKEN":"old","ENABLE_TOOL_SEARCH":"old"}}`)
			service.options.ServerPort = 5178
			service.options.ProxyReady = true
			p, err := service.Provider(ctx, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := service.buildManagedEnv(p, "/proxy/providers/fixture")
			if err != nil {
				t.Fatal(err)
			}
			if err = service.Activate(ctx, "fixture"); err != nil {
				t.Fatal(err)
			}
			managed, err := service.options.Managed.ReadSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var actual any
			_ = json.Unmarshal(encode(map[string]any{"runtime": runtime, "settings": managed}), &actual)
			if !reflect.DeepEqual(actual, expected[i]) {
				t.Errorf("Source%s Go%s", encode(expected[i]), encode(actual))
			}
		})
	}
}
