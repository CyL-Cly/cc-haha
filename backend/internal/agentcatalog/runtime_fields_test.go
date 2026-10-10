package agentcatalog

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

func TestAgentDefinitionRuntimeFieldsMatchActualMarkdownSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.Mkdir(config, 0700)
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	type example struct {
		Frontmatter string `json:"frontmatter"`
		Body        string `json:"body"`
		FilePath    string `json:"filePath"`
		BaseDir     string `json:"baseDir"`
		Source      string `json:"source"`
		Hash        string `json:"hash"`
		Memory      bool   `json:"memoryEnabled"`
	}
	base := `"name":"review-worker","description":"Review carefully"`
	fronts := []string{
		`{` + base + `}`,
		`{` + base + `,"tools":null}`,
		`{` + base + `,"tools":"Read, Grep Bash(git status, log)","disallowedTools":["Write","Edit"]}`,
		`{` + base + `,"tools":["Grep","*","Read"],"disallowedTools":["*"]}`,
		`{` + base + `,"tools":["Read",true,null,7,"Read"],"skills":"test, explain","color":"cyan","permissionMode":"plan","maxTurns":"12suffix","background":"true","initialPrompt":"  start here  ","model":"  inherit  ","effort":" high "}`,
		`{` + base + `,"tools":false,"skills":["*"],"color":"invalid","permissionMode":"not-mode","maxTurns":"bad","background":false,"initialPrompt":4}`,
		`{` + base + `,"tools":["Bash"],"memory":"project"}`,
		`{` + base + `,"tools":["Bash"],"memory":"project"}`,
		`{` + base + `,"tools":[],"memory":"user","model":"other-custom-model","effort":-1,"isolation":"worktree"}`,
		`{` + base + `,"tools":[],"memory":"invalid","isolation":"remote","maxTurns":2.5}`,
		`{` + base + `,"hooks":{"PreToolUse":[{"matcher":"Read","ignored":true,"hooks":[{"type":"command","command":"echo fixture","if":"Read(*.go)","timeout":2,"shell":"bash","once":false,"async":true,"ignored":true}]}],"Stop":[{"hooks":[{"type":"prompt","prompt":"Verify","model":"inherit","statusMessage":"running"},{"type":"agent","prompt":"Review","timeout":2}]}]}}`,
		`{` + base + `,"hooks":{"Stop":[{"hooks":[{"type":"http","url":"http://127.0.0.1:1/hook","headers":{"X-Fixture":"fake"},"allowedEnvVars":["FIXTURE"],"once":true}]}]}}`,
		`{` + base + `,"hooks":{"Stop":[{"hooks":[{"type":"command","command":"invalid","timeout":0}]}]}}`,
		`{` + base + `,"hooks":{"Unknown":[{"hooks":[]}]},"mcpServers":["fixture",{"stdio":{"command":"echo","args":["fixture"],"unknown":true}},false,{"http":{"type":"http","url":"http://127.0.0.1:1/mcp"}},{"invalid":{"type":"unknown"}}]}`,
	}
	inputs := []example{}
	actual := []json.RawMessage{}
	for i, front := range fronts {
		item := example{Frontmatter: front, Body: " \nReview only the requested files.\r\n ", FilePath: filepath.Join(config, "agents", "nested", "review.md"), BaseDir: filepath.Join(config, "agents"), Source: "projectSettings", Hash: "fixture-content-hash", Memory: i != 7}
		inputs = append(inputs, item)
		var parsed protocol.JSONObject
		_ = parsed.UnmarshalJSON([]byte(front))
		definition, why, err := parse(item.FilePath, item.Source, parsed, item.Body)
		if err != nil || definition == nil {
			t.Fatalf("native parse %d %s %v", i, why, err)
		}
		definition.ContentHash, definition.BaseDir = item.Hash, item.BaseDir
		if err := enrichMarkdownDefinition(definition, item.Memory); err != nil {
			t.Fatal(err)
		}
		raw, _ := definition.Fields.MarshalJSON()
		actual = append(actual, protocol.StringifyValidatedJavaScriptJSON(raw))
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-definition.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_AGENT_DEFINITION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cases": inputs})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Agent Source %v %.2200s", err, stderr.String())
	}
	var source struct {
		Definitions  []*struct{ Fields string }
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(output, &source) != nil || len(source.Definitions) != len(inputs) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped or invalid %.2200s", output)
	}
	for i, definition := range source.Definitions {
		if definition == nil || string(actual[i]) != definition.Fields {
			t.Errorf("actual source fields case%d\nGo %s\nSource %+v", i, actual[i], definition)
		}
	}
}
