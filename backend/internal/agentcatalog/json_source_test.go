package agentcatalog

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

func TestFrozenJSONAgentDefinitionMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	type scenario struct {
		Definition protocol.JSONObject
		Memory     bool
	}
	base := func() protocol.JSONObject {
		var v protocol.JSONObject
		_ = v.UnmarshalJSON([]byte(`{"description":"  Exact description\\nkeep  ","prompt":"  Approved frozen prompt  ","tools":["Read, Bash(ls -*)","Write"],"disallowedTools":["Bash(rm *)"],"skills":["skill with spaces"],"model":" InHerIt ","effort":"high","permissionMode":"default","mcpServers":["named",{"inline":{"command":"fake-never-executed","args":[]}}],"hooks":{"Stop":[]},"maxTurns":3,"initialPrompt":"  preserve  ","memory":"project","background":false,"omitClaudeMd":false,"color":"cyan","isolation":"worktree","future":"strip"}`))
		return v
	}
	cases := []scenario{{base(), false}, {base(), true}}
	for _, patch := range []string{`{"effort":" HIGH "}`, `{"effort":"3"}`, `{"effort":-1}`, `{"effort":9007199254740992}`, `{"tools":"Read"}`, `{"tools":["*"]}`, `{"tools":[]}`, `{"description":"  "}`, `{"prompt":null}`, `{"model":" "}`, `{"skills":[]}`, `{"mcpServers":[]}`, `{"mcpServers":[{"inline":{"type":"unknown"}}]}`, `{"hooks":{"unknown":[]}}`, `{"maxTurns":1.5}`, `{"initialPrompt":""}`, `{"background":true}`, `{"omitClaudeMd":true}`, `{"permissionMode":"bad"}`, `{"memory":"bad"}`, `{"color":"bad"}`, `{"isolation":"remote"}`} {
		var p protocol.JSONObject
		_ = p.UnmarshalJSON([]byte(patch))
		v := base()
		v.Assign(p)
		cases = append(cases, scenario{v, true})
	}
	home := t.TempDir()
	for _, p := range []string{".claude", "tmp"} {
		_ = os.MkdirAll(filepath.Join(home, p), 0700)
	}
	rows := []map[string]any{}
	for _, c := range cases {
		rows = append(rows, map[string]any{"name": "worker-研究", "definition": c.Definition, "memory": c.Memory})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-json.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AGENT_JSON_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(encodeField(map[string]any{"homeDir": home, "cases": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(out))
	}
	for i, c := range cases {
		d, e := ParseJSONDefinition("worker-研究", encodeField(c.Definition), "flagSettings", "", c.Memory)
		if e != nil {
			t.Fatal(e)
		}
		var actual json.RawMessage = []byte(`null`)
		if d != nil {
			actual = encodeField(d.Fields)
		}
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%d %s\n%s", i, actual, source.Rows[i])
		}
	}
}
