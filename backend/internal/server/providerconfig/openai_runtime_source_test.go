package providerconfig

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

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestOpenAIOfficialActivateAndRuntimeActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source comparison requires pinned Bun")
	}
	service, config := fixture(t)
	home := filepath.Dir(config)
	base := map[string]string{"USER_OWNED": "retained", "ANTHROPIC_API_KEY": "fake-unrelated-key", "ANTHROPIC_AUTH_TOKEN": "fake-unrelated-token", "CC_HAHA_IMAGE_PROVIDER_KIND": "old-image-kind", "ENABLE_TOOL_SEARCH": "true"}
	rows := []map[string]any{}
	for _, mapping := range []any{
		nil,
		map[string]any{"main": " gpt-6.1-sol ", "haiku": " ", "sonnet": " custom-sonnet ", "opus": "", "fable": " ", "unknown": true},
		map[string]any{"main": "gpt-5.4", "haiku": "gpt-6-luna", "sonnet": "gpt-5.5", "opus": "gpt-6-astra", "fable": "custom-fable"},
	} {
		index := storage.Object{"schemaVersion": encode(6), "providers": encode([]storage.Object{saved("openai-official")}), "activeId": encode("openai-official"), "future": encode(map[string]any{"retained": true})}
		if mapping != nil {
			index["officialProviderModels"] = encode(map[string]any{"openai-official": mapping, "future-provider": map[string]any{"keep": true}})
		}
		rows = append(rows, map[string]any{"index": index, "managed": map[string]any{"futureSettings": map[string]any{"keep": true}, "model": "stale-model", "modelContext": "1m", "env": base}})
	}
	fixturePath, _ := filepath.Abs("../../../../scripts/quality-gate/go-parity/native-openai-provider-runtime.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"rows": rows, "base": base})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source provider runtime %v\n%s", err, stderr.String())
	}
	var expected []any
	if json.Unmarshal(output, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source provider runtime output %s", output)
	}
	for i, row := range rows {
		data, _ := json.Marshal(row["index"])
		seed(t, service.indexPath(), string(data))
		data, _ = json.Marshal(row["managed"])
		seed(t, filepath.Join(config, "cc-haha", "settings.json"), string(data))
		provider, err := service.Provider(ctx, "openai-official")
		if err != nil {
			t.Fatal(err)
		}
		runtime, err := service.buildManagedEnv(provider)
		if err != nil {
			t.Fatal(err)
		}
		if err = service.Activate(ctx, "openai-official"); err != nil {
			t.Fatal(err)
		}
		resolved, err := ResolveRuntimeEnv(ctx, service.options.Store, config, base, nil)
		if err != nil {
			t.Fatal(err)
		}
		index, err := service.ReadIndex(ctx)
		if err != nil {
			t.Fatal(err)
		}
		managed, err := service.options.Managed.ReadSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		data, _ = json.Marshal(map[string]any{"provider": provider, "runtime": runtime, "resolved": resolved, "index": index, "managed": managed})
		var actual any
		_ = json.Unmarshal(data, &actual)
		if !reflect.DeepEqual(actual, expected[i]) {
			want, _ := json.Marshal(expected[i])
			t.Errorf("row %d\nSource %s\nGo %s", i, want, data)
		}
	}
}
