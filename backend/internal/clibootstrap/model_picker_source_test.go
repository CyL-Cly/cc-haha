package clibootstrap

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeModelPickerOptionsEffortAndPricingMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source requires explicitly supplied Bun 1.3.14")
	}
	type row struct {
		Name       string              `json:"name"`
		Env        map[string]string   `json:"env"`
		Settings   protocol.JSONObject `json:"settings"`
		Override   *string             `json:"override"`
		Initial    *string             `json:"initial"`
		Additional []map[string]any    `json:"additional"`
	}
	rows := []row{
		{Name: "default-third-party", Settings: protocol.NewJSONObject()},
		{Name: "raw-deepseek", Override: pickerPointer("deepseek-v4-flash"), Initial: pickerPointer("deepseek-v4-flash"), Settings: protocol.NewJSONObject()},
		{Name: "custom-tiers-empty-label", Env: map[string]string{"ANTHROPIC_DEFAULT_FABLE_MODEL": "relay-fable[1m]", "ANTHROPIC_DEFAULT_SONNET_MODEL": "deepseek-v4-flash", "ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "", "ANTHROPIC_DEFAULT_OPUS_MODEL": "deepseek-v4-pro", "ANTHROPIC_DEFAULT_OPUS_MODEL_DESCRIPTION": "中文模型👋", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "relay-haiku", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES": "effort,max_effort"}, Override: pickerPointer("sonnet"), Settings: protocol.NewJSONObject()},
		{Name: "custom-option-cache-deduplicate", Env: map[string]string{"ANTHROPIC_CUSTOM_MODEL_OPTION": "relay-extra", "ANTHROPIC_CUSTOM_MODEL_OPTION_NAME": "extra name"}, Additional: []map[string]any{{"value": "relay-extra", "label": "duplicate", "description": "ignored"}, {"value": "cache-model", "label": "缓存模型", "description": "cached"}}, Settings: protocol.NewJSONObject()},
		{Name: "allowlist-raw-current", Override: pickerPointer("deepseek-v4-flash"), Initial: pickerPointer("deepseek-v4-flash"), Settings: pickerTestObject(`{"availableModels":["haiku","claude-sonnet-4-6"]}`)},
		{Name: "disabled-1m", Env: map[string]string{"CLAUDE_CODE_DISABLE_1M_CONTEXT": "true"}, Settings: protocol.NewJSONObject()},
		{Name: "known-legacy-sonnet", Override: pickerPointer("claude-sonnet-4-20250514"), Settings: protocol.NewJSONObject()},
		{Name: "known-pinned-fable51", Override: pickerPointer("claude-fable-5-1"), Settings: protocol.NewJSONObject()},
		{Name: "opus-plan", Override: pickerPointer("opusplan"), Settings: protocol.NewJSONObject()},
		{Name: "opus-1m-current", Override: pickerPointer("opus[1m]"), Settings: protocol.NewJSONObject()},
		{Name: "default-retains-initial", Initial: pickerPointer("prior-explicit-model"), Settings: protocol.NewJSONObject()},
		{Name: "model-overrides", Settings: pickerTestObject(`{"modelOverrides":{"claude-sonnet-4-6":"deployment-sonnet","claude-opus-4-7":"deployment-opus"}}`)},
	}
	prices := []string{"claude-opus-5-5", "claude-fable-5", "claude-3-5-haiku", "claude-sonnet-4-6", "claude-opus-4-1", "claude-haiku-4-5-20251001", "unknown"}
	home := t.TempDir()
	for _, leaf := range []string{"tmp", "config", "cache"} {
		if err := os.Mkdir(filepath.Join(home, leaf), 0700); err != nil {
			t.Fatal(err)
		}
	}
	source, err := filepath.Abs("testdata/model_picker.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"home": home, "cases": rows, "prices": prices})
	cmd := exec.Command(bun, "--no-env-file", source)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = []string{"BUN_OPTIONS=--no-env-file", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "PATH=/usr/bin:/bin", "ANTHROPIC_API_KEY=fixture-no-network", "ANTHROPIC_BASE_URL=http://127.0.0.1:43210", "CC_HAHA_MODEL_PICKER_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "NODE_ENV=production", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "DISABLE_TELEMETRY=1"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil || stderr.Len() > 0 {
		t.Fatalf("actual Source failed %v: %s", err, stderr.String())
	}
	type option struct {
		Value          *string `json:"value"`
		Label          string  `json:"label"`
		Description    string  `json:"description"`
		SupportsEffort bool    `json:"supportsEffort"`
		SupportsMax    bool    `json:"supportsMax"`
		DefaultEffort  string  `json:"defaultEffort"`
	}
	var want struct {
		Rows []struct {
			Name    string
			Options []option
		}
		Prices       []*string
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &want); err != nil {
		t.Fatal(err, string(output))
	}
	if len(want.Capabilities.Blocked) > 0 {
		t.Fatal("Source fixture escaped boundary", want.Capabilities.Blocked)
	}
	if len(want.Rows) != len(rows) {
		t.Fatal("Source rows", string(output))
	}
	for i, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			env := map[string]string{"ANTHROPIC_API_KEY": "fixture-no-network", "ANTHROPIC_BASE_URL": "http://127.0.0.1:43210", "CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_DISABLE_FAST_MODE": "1"}
			for key, value := range row.Env {
				env[key] = value
			}
			var override json.RawMessage = []byte("null")
			if row.Override != nil {
				override = quoteMain(*row.Override)
			}
			selector, err := modelselection.NewDirectCredentials(modelselection.Options{Environment: env, Settings: row.Settings, Override: override})
			if err != nil {
				t.Fatal(err)
			}
			global := protocol.NewJSONObject()
			data, _ := json.Marshal(row.Additional)
			if row.Additional != nil {
				global.Set("additionalModelOptionsCache", data)
			}
			native, err := nativePickerOptions(env, row.Settings, &global, selector, row.Initial)
			if err != nil {
				t.Fatal(err)
			}
			var got []option
			for _, item := range native {
				resolved, err := nativePickerResolve(selector, item.Value)
				if err != nil {
					t.Fatal(err)
				}
				supports, max, def, err := nativePickerEffort(selector, resolved, env)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, option{item.Value, item.Label, item.Description, supports, max, def})
			}
			if !reflect.DeepEqual(got, want.Rows[i].Options) {
				g, _ := json.Marshal(got)
				w, _ := json.Marshal(want.Rows[i].Options)
				t.Fatalf("native menu differs\nGo %s\nSource %s", g, w)
			}
		})
	}
	for i, model := range prices {
		canonical := model
		if model == "claude-haiku-4-5-20251001" {
			canonical = "claude-haiku-4-5"
		}
		got, found := modelaccounting.NativeModelPricingString(canonical)
		if found != (want.Prices[i] != nil) || found && got != *want.Prices[i] {
			t.Fatal("actual Source pricing format", model, got, want.Prices[i])
		}
	}
}
func pickerTestObject(value string) protocol.JSONObject {
	var result protocol.JSONObject
	if err := result.UnmarshalJSON([]byte(value)); err != nil {
		panic(err)
	}
	return result
}
