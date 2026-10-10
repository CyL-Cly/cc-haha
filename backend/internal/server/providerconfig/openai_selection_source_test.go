package providerconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestDesktopOpenAIRuntimeSelectionActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source comparison requires pinned Bun")
	}
	service, config := fixture(t)
	home := filepath.Dir(config)
	rows := []map[string]any{}
	for _, model := range []string{" gpt-6-sol ", "GPT-5.4", "custom-unknown", "\ufeff gpt-6.1-sol \u00a0", " "} {
		for _, effort := range []any{nil, " low ", "high", "max", "ultra", ""} {
			input := storage.Object{"providerId": encode("openai-official"), "modelId": encode(model)}
			if effort != nil {
				input["effortLevel"] = encode(effort)
			}
			rows = append(rows, map[string]any{"input": input})
		}
	}
	for _, managed := range []any{map[string]any{}, map[string]any{"model": "gpt-6-astra"}, map[string]any{"model": "gpt-5.4", "modelContext": "1m"}} {
		rows = append(rows, map[string]any{"input": storage.Object{"providerId": encode("deleted"), "modelId": encode(" stale "), "effortLevel": encode("high")}, "managed": managed})
	}
	remote := []providerproxy.OpenAIModel{{Value: "remote-model", Label: "Remote", Description: "private catalog fixture", DefaultReasoningEffort: "high", SupportedReasoningEfforts: []string{"medium", "high"}, ContextWindow: 123456}}
	for _, effort := range []string{"low", "high", "max"} {
		rows = append(rows, map[string]any{"input": storage.Object{"providerId": encode("openai-official"), "modelId": encode("remote-model"), "effortLevel": encode(effort)}, "catalog": remote})
	}
	fixturePath, _ := filepath.Abs("../../../../scripts/quality-gate/go-parity/native-openai-desktop-selection.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin", "NODE_ENV=production", "DISABLE_TELEMETRY=1"}
	input, _ := json.Marshal(map[string]any{"rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source runtime selection %v\n%s", err, stderr.String())
	}
	var expected struct {
		Observations []any `json:"observations"`
		Capabilities struct {
			Blocked []string `json:"blocked"`
		} `json:"capabilities"`
	}
	if json.Unmarshal(output, &expected) != nil || len(expected.Observations) != len(rows) || len(expected.Capabilities.Blocked) != 0 {
		t.Fatalf("Source selection output %s", output)
	}
	for i, row := range rows {
		seed(t, service.indexPath(), `{"schemaVersion":6,"activeId":"openai-official","providers":[],"officialProviderModels":{}}`)
		data, _ := json.Marshal(row["managed"])
		if row["managed"] == nil {
			data = []byte(`{}`)
		}
		seed(t, filepath.Join(config, "cc-haha", "settings.json"), string(data))
		seed(t, filepath.Join(config, "settings.json"), `{"effort":"max","model":"unrelated-global-model"}`)
		calls := 0
		service.options.OpenAIModelCatalog = func(context.Context) ([]providerproxy.OpenAIModel, error) {
			calls++
			if catalog, ok := row["catalog"].([]providerproxy.OpenAIModel); ok {
				return catalog, nil
			}
			return providerproxy.OpenAIModelCatalog(), nil
		}
		selection, err := NormalizeRuntimeSelection(ctx, service.options, row["input"].(storage.Object))
		result := map[string]any{"ok": true}
		if err != nil {
			var api *settings.APIError
			if !errors.As(err, &api) || api.Code != "RUNTIME_CONFIG_INVALID" {
				t.Fatal(err)
			}
			reason := "effort"
			if strings.Contains(api.Message, "model") {
				reason = "model"
			}
			result = map[string]any{"ok": false, "reason": reason}
		} else {
			override := map[string]any{"providerId": selection.ProviderID, "modelId": selection.ModelID}
			if selection.EffortLevel != "" {
				override["effort"] = selection.EffortLevel
			}
			if selection.RequestedConfig != nil {
				override["requestedConfig"] = selection.RequestedConfig
			}
			result["override"] = override
		}
		data, _ = json.Marshal(map[string]any{"result": result, "catalogCalls": calls})
		var actual any
		_ = json.Unmarshal(data, &actual)
		if !reflect.DeepEqual(actual, expected.Observations[i]) {
			t.Errorf("row %d Source %v Go %s", i, expected.Observations[i], data)
		}
	}
}
