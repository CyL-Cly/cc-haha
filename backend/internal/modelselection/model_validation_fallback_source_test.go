package modelselection

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestModelValidationFallbackActualOriginalProviderStringsAndOverrides(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("explicit pinned Source required")
	}
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	rows := []map[string]any{}
	envs := []map[string]string{}
	settings := []protocol.JSONObject{}
	for _, provider := range []string{"firstParty", "bedrock", "vertex", "foundry", "azureOpenAI"} {
		for _, override := range []bool{false, true} {
			env := map[string]string{}
			for k, v := range map[string]string{"bedrock": "CLAUDE_CODE_USE_BEDROCK", "vertex": "CLAUDE_CODE_USE_VERTEX", "foundry": "CLAUDE_CODE_USE_FOUNDRY", "azureOpenAI": "CLAUDE_CODE_USE_AZURE_OPENAI"} {
				if provider == k {
					env[v] = "1"
				}
			}
			value := protocol.NewJSONObject()
			if override {
				value.Set("modelOverrides", []byte(`{"claude-opus-4-1-20250805":"owned-Opus-profile","claude-sonnet-4-5-20250929":"owned-Sonnet45-profile","claude-sonnet-4-20250514":"owned-Sonnet40-profile"}`))
			}
			rows = append(rows, map[string]any{"provider": provider, "env": env, "settings": value})
			envs = append(envs, env)
			settings = append(settings, value)
		}
	}
	fixture, _ := filepath.Abs("testdata/model-validation-strings.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/loopback-only.sb")
	payload, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd := exec.Command("/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MODEL_FALLBACK_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1"}
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source struct{ Rows [][]string }
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(rows) {
		t.Fatal(e, string(out))
	}
	for i, env := range envs {
		selector, e := NewParsing(Options{Environment: env, Settings: settings[i]})
		if e != nil {
			t.Fatal(e)
		}
		for j, canonical := range []string{"claude-opus-4-1-20250805", "claude-sonnet-4-5-20250929", "claude-sonnet-4-20250514"} {
			if got := selector.ValidationFallbackID(canonical); got != source.Rows[i][j] {
				t.Fatal("actual original 3P fallback key", i, j, got, source.Rows[i][j])
			}
		}
	}
}
