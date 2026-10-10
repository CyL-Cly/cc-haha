package modelselection

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

func TestOpenAIModelSelectionAgainstActualSavedIdentitySource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	type row struct {
		Env      map[string]string   `json:"env"`
		Settings protocol.JSONObject `json:"settings"`
		Override json.RawMessage     `json:"override,omitempty"`
		Parse    []string            `json:"parse"`
	}
	cases := []row{}
	for _, env := range []map[string]string{
		{}, {"ANTHROPIC_BASE_URL": "https://private.invalid"},
		{"ANTHROPIC_DEFAULT_OPUS_MODEL": "competing-opus", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "competing-haiku", "ANTHROPIC_DEFAULT_SONNET_MODEL": "competing-sonnet", "ANTHROPIC_DEFAULT_FABLE_MODEL": "competing-fable"},
		{"OPENAI_CODEX_MODEL": "  private-all  "},
		{"OPENAI_CODEX_HAIKU_MODEL": "private-haiku", "OPENAI_CODEX_SONNET_MODEL": "private-sonnet", "OPENAI_CODEX_OPUS_MODEL": "private-opus"},
		{"OPENAI_CODEX_MODEL": " \ufeff ", "CLAUDE_CODE_DISABLE_1M_CONTEXT": "1"},
		{"ANTHROPIC_SMALL_FAST_MODEL": "private-small", "CLAUDE_CODE_SUBAGENT_MODEL": "sonnet"},
	} {
		for _, specified := range []string{"", "haiku", "opusplan", " gpt-6-sol ", "opus[1m]"} {
			r := row{Env: cloneEnvironment(env), Settings: protocol.NewJSONObject(), Parse: []string{"fable", "best", "sonnet", "opus", "haiku", "opusplan", " sonnet[1m] ", "claude-opus-4-20250514", "gpt-6-astra", " private-model "}}
			if specified != "" {
				r.Override = quote(specified)
			}
			cases = append(cases, r)
		}
	}
	for _, settings := range []string{`{"model":"haiku"}`, `{"model":"opus","availableModels":["private-only"]}`, `{"model":"haiku","availableModels":["haiku"]}`, `{"model":"sonnet","modelOverrides":{"claude-sonnet-5-5":"private-override"}}`} {
		var parsed protocol.JSONObject
		_ = parsed.UnmarshalJSON([]byte(settings))
		cases = append(cases, row{Env: map[string]string{}, Settings: parsed, Parse: []string{"opus", "sonnet", "haiku"}})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-model-selection.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_OPENAI_MODEL_SELECTION_FIXTURE=1", "CLAUDE_CODE_SIMPLE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	raw, _ := json.Marshal(map[string]any{"home": home, "cases": cases})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	actual, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source %v %s", err, stderr.String())
	}
	type observation struct {
		Selected  string   `json:"selected"`
		Default   string   `json:"default"`
		SmallFast string   `json:"smallFast"`
		Parsed    []string `json:"parsed"`
		Runtime   string   `json:"runtime"`
		Agent     string   `json:"agent"`
	}
	var corpus struct {
		Rows         []observation `json:"rows"`
		Capabilities struct {
			Blocked []string `json:"blocked"`
		} `json:"capabilities"`
		Consumer string `json:"consumer"`
	}
	if err = json.Unmarshal(actual, &corpus); err != nil || len(corpus.Rows) != len(cases) || len(corpus.Capabilities.Blocked) != 0 || corpus.Consumer != "src/utils/model/model.ts + auth.ts + model/agent.ts" {
		t.Fatalf("original corpus %v %s", err, actual)
	}
	for i, r := range cases {
		s, err := NewOpenAIAuthentication(Options{Environment: r.Env, Settings: r.Settings, Override: r.Override})
		if err != nil {
			t.Fatal(err)
		}
		decode := func(value json.RawMessage, e error) string {
			t.Helper()
			if e != nil {
				t.Fatal(e)
			}
			var v string
			if e = json.Unmarshal(value, &v); e != nil {
				t.Fatal(e)
			}
			return v
		}
		got := observation{Selected: decode(s.Select()), Default: decode(s.DefaultSetting(), nil), SmallFast: s.SmallFastModel()}
		for _, value := range r.Parse {
			got.Parsed = append(got.Parsed, decode(s.Parse(quote(value))))
		}
		got.Runtime = decode(s.RuntimeModel(quote("private-parent"), "plan", false))
		got.Agent = decode(s.AgentModel(AgentInput{AgentModel: quote("haiku"), ParentModel: quote("private-parent"), PermissionMode: "default"}))
		if !reflect.DeepEqual(got, corpus.Rows[i]) {
			t.Errorf("case%d env%v override%s got%+v want%+v", i, r.Env, r.Override, got, corpus.Rows[i])
		}
	}
}
