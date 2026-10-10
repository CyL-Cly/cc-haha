package agentcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPluginAgentTrustAndFieldsMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	type sample struct {
		Content string `json:"content"`
		Memory  bool   `json:"memoryEnabled"`
	}
	cases := []sample{
		{"Agent body with invalid UTF-8: \xff.", false},
		{"Agent body without metadata.", false},
		{"---\nname: reviewer\ndescription: Review the sample\ntools: Read, Bash(git status)\ncolor: cyan\nmodel: ' inherit '\n---\nRead ${CLAUDE_PLUGIN_ROOT}.", false},
		{"---\nname: 7\ndescription: false\ncolor: invalid-plugin-color\nmodel: OtherModel\n---\nBody.", false},
		{"---\nname: [one, two]\ndescription: ' '\nwhen-to-use: true\ncolor: false\n---\nBody.", false},
		{"---\ntools: []\nmemory: project\nbackground: true\nisolation: worktree\neffort: high\nmaxTurns: 9suffix\n---\nMemory body.", true},
		{"---\ntools: []\nmemory: project\n---\nMemory disabled.", false},
		{"---\npermissionMode: bypassPermissions\nhooks:\n  Stop:\n    - hooks:\n        - type: command\n          command: echo fixture\nmcpServers: [fixture]\ninitialPrompt: 4\ndisallowedTools: [Write, Edit]\nskills: one, two\n---\nPlugin trust boundary.", false},
	}
	actual := []string{}
	for i, row := range cases {
		file := filepath.Join(home, "plugin", "agents", "fixture-"+strconv.Itoa(i)+".md")
		definition, err := PluginDefinition(file, "sample", "sample@fixture", []string{"nested"}, []byte(row.Content), row.Memory, func(body string) (string, error) {
			return strings.ReplaceAll(body, "${CLAUDE_PLUGIN_ROOT}", filepath.Join(home, "plugin")), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := definition.Fields.MarshalJSON()
		actual = append(actual, string(protocol.StringifyValidatedJavaScriptJSON(data)))
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-plugin.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_AGENT_PLUGIN_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "cases": cases})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source plugin %v %.1800s", err, stderr.String())
	}
	var source struct {
		Rows         []*string
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(out, &source) != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source invalid/escaped %.1800s", out)
	}
	for i, row := range source.Rows {
		if row == nil || *row != actual[i] {
			t.Errorf("case%d Go %s Source %v", i, actual[i], row)
			if row != nil {
				t.Log(*row)
			}
		}
	}
}
