package teamspawn

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestInProcessTeammateDefinitionAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	definitions := []string{"", `{}`, `{"tools":[]}`, `{"tools":["Read","SendMessage","Read","TeamCreate"],"model":"sonnet","effort":"high","hooks":{"Stop":[]},"skills":["unused"],"permissionMode":"plan"}`, `{"tools":["*"],"effort":0}`, `{"tools":["Agent(Explore)","Write"],"model":"","effort":null}`}
	rows := []map[string]any{}
	actual := []map[string]any{}
	for _, source := range definitions {
		row := map[string]any{"name": "actual-worker", "system": "Actual full system\nTeammate addendum"}
		var definition *agentcatalog.Definition
		if source != "" {
			var fields protocol.JSONObject
			if err := fields.UnmarshalJSON([]byte(source)); err != nil {
				t.Fatal(err)
			}
			row["definition"] = fields
			definition = &agentcatalog.Definition{Fields: fields, Model: fields.String("model")}
		}
		rows = append(rows, row)
		d := InProcessDefinition(row["name"].(string), row["system"].(string), definition)
		actual = append(actual, map[string]any{"fields": d.Fields, "prompt": d.SystemPrompt})
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-inprocess-teammate-preset.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INPROCESS_TEAM_PRESET_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	raw, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	for i, row := range actual {
		raw, _ := json.Marshal(row)
		var a, b any
		_ = json.Unmarshal(raw, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case%d Go%s Source%s", i, raw, source.Rows[i])
		}
	}
}
