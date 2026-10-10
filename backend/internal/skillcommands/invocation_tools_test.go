package skillcommands

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

func TestInvocationToolGrammarPreservesActualSourceRules(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	inputs := []string{`[]`, `[""]`, `["Read, Write  Edit"]`, `["Bash(echo a,b) Read","Skill(/review:*)"]`, `["\t\ufeffRead\u00a0","Read\tWrite","Read\nWrite"]`, `["Task", "Skill(*)", "Skill()", "unknown"]`, `["Bash(a(b), x) Edit", "Bash(a\\) b) Read"]`, `["\ud800,\udfff", "mcp__x__tool(a,b)"]`, `["  , ,  \t  ,Read  ,"]`}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "toolLists": inputs})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		ToolLists    []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.ToolLists) != len(inputs) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	for i, input := range inputs {
		got, e := ParseInvocationTools([]byte(input))
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(got)
		if string(protocol.StringifyValidatedJavaScriptJSON(raw)) != string(protocol.StringifyValidatedJavaScriptJSON(source.ToolLists[i])) {
			t.Errorf("input%d Go%s Source%s", i, raw, source.ToolLists[i])
		}
	}
}
