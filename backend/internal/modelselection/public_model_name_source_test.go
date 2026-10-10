package modelselection

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestPublicModelDisplayNameMatchesExactOriginalProviderIDsAndOverrides(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source requires pinned Bun")
	}
	type profile struct {
		Provider string              `json:"provider"`
		Env      map[string]string   `json:"env"`
		Settings protocol.JSONObject `json:"settings"`
	}
	profiles := []profile{}
	for _, item := range []struct{ provider, flag string }{{"firstParty", ""}, {"bedrock", "CLAUDE_CODE_USE_BEDROCK"}, {"vertex", "CLAUDE_CODE_USE_VERTEX"}, {"foundry", "CLAUDE_CODE_USE_FOUNDRY"}, {"azureOpenAI", "CLAUDE_CODE_USE_AZURE_OPENAI"}} {
		for _, settings := range []string{`{}`, `{"modelOverrides":{"claude-sonnet-4-6":"relay-sonnet","claude-opus-4-7":"relay-opus","claude-haiku-4-5-20251001":"relay-haiku"},"future":{"keep":true}}`, `{"modelOverrides":{"claude-opus-4-7":"shared-relay","claude-fable-5-1":"shared-relay","claude-sonnet-4-20250514":"relay-legacy","unknown":"claude-opus-4-7"}}`} {
			var object protocol.JSONObject
			if e := object.UnmarshalJSON([]byte(settings)); e != nil {
				t.Fatal(e)
			}
			env := map[string]string{}
			if item.flag != "" {
				env[item.flag] = "1"
			}
			profiles = append(profiles, profile{item.provider, env, object})
		}
	}
	values := []json.RawMessage{}
	for _, value := range []string{"claude-opus-4-5", "claude-opus-4-1", "claude-opus-4", "claude-3-5-haiku", "claude-haiku-4-5", "claude-sonnet-4-5", "claude-3-7-sonnet", "third-party-unknown", "opus", "sonnet", "gpt-6", "gpt-6.1-sol", "gpt-5.6-terra", " GPT-5.3-CODEX ", "gpt-5.5-pro", "gpt-5.4-pro", "gpt-5.4-nano", "gpt-5.1", "gpt-5-codex", "中文-claude-opus-4-7", "claude-opus-4-7👋"} {
		values = append(values, quote(value))
	}
	values = append(values, json.RawMessage(`"\ud800claude-opus-4-7"`), json.RawMessage(`"claude-opus-4-7\udc00"`))
	home := t.TempDir()
	fixture, e := filepath.Abs("testdata/public-model-name.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PUBLIC_MODEL_NAME_FIXTURE=1", "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": profiles, "values": values})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("original public display: %v %s", e, stderr.String())
	}
	var original struct {
		Rows []struct {
			Values []json.RawMessage `json:"values"`
			Labels []*string         `json:"labels"`
		} `json:"rows"`
		Capabilities struct{ Blocked []string }
		Consumer     string
	}
	if e = json.Unmarshal(out, &original); e != nil || len(original.Rows) != len(profiles) || len(original.Capabilities.Blocked) != 0 || original.Consumer != "src/utils/model/model.ts:getPublicModelDisplayName + modelStrings.ts:getModelStrings" {
		t.Fatalf("actual Source corpus: %v %s", e, out)
	}
	observations := 0
	for i, profile := range profiles {
		selector, e := NewParsing(Options{Environment: profile.Env, Settings: profile.Settings})
		if e != nil {
			t.Fatal(e)
		}
		// Constructor owns a frozen snapshot; callers cannot change its IDs by
		// mutating the supplied environment/settings after construction.
		profile.Env["CLAUDE_CODE_USE_VERTEX"] = "1"
		profile.Settings.Set("modelOverrides", []byte(`{"claude-opus-4-7":"changed-after-snapshot"}`))
		row := original.Rows[i]
		if len(row.Values) == 0 || len(row.Values) != len(row.Labels) {
			t.Fatal("missing original labels")
		}
		for j, raw := range row.Values {
			got, e := selector.PublicModelDisplayName(raw)
			if e != nil {
				t.Fatalf("profile%d raw%s: %v", i, raw, e)
			}
			want := ""
			if row.Labels[j] != nil {
				want = *row.Labels[j]
			}
			if got != want {
				t.Errorf("profile%d/%s raw%s Go=%q Source=%q", i, profile.Provider, raw, got, want)
			}
			observations++
		}
	}
	t.Logf("%d actual Source display observations across %d frozen provider/settings profiles", observations, len(profiles))
}
