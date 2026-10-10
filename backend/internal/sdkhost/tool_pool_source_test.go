package sdkhost

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

	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func TestActualSourceFinalBareToolPoolDiffersFromLowerLevelRegistrationOrder(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	profiles := []map[string]any{}
	for _, simple := range []string{"1", "TRUE", " yes ", "on"} {
		for _, deny := range [][]string{{}, {"Read"}, {"Bash"}, {"Edit"}, {"Bash", "Read", "Edit"}} {
			profiles = append(profiles, map[string]any{"env": map[string]string{"CLAUDE_CODE_SIMPLE": simple, "CC_HAHA_AGENT_TEAMS_ENABLED": "false"}, "nonInteractive": true, "deny": deny})
		}
	}
	profiles = append(profiles, map[string]any{"env": map[string]string{"CLAUDE_CODE_SIMPLE": "1"}, "nonInteractive": true, "mcp": []map[string]any{{"name": "mcp__zeta__read", "isMcp": true}, {"name": "mcp__alpha__read", "isMcp": true}, {"name": "mcp__alpha__read", "isMcp": true}, {"name": "Read", "isMcp": true}}})
	profiles = append(profiles, map[string]any{"env": map[string]string{"CLAUDE_CODE_SIMPLE": "1"}, "nonInteractive": true, "mcp": []map[string]any{{"name": "mcp__10__read", "isMcp": true}, {"name": "mcp__2__read", "isMcp": true}, {"name": "mcp__á__read", "isMcp": true}, {"name": "mcp__Alpha__Read", "isMcp": true}, {"name": "mcp__alpha__read", "isMcp": true}, {"name": "mcp__å__read", "isMcp": true}, {"name": "mcp___underscore__read", "isMcp": true}, {"name": "mcp__数字__read", "isMcp": true}}})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-tool-pool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_TOOL_POOL_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-tool-pool-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "profiles": profiles})
	cmd.Stdin = bytes.NewReader(data)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, out)
	}
	var source struct {
		Profiles     []struct{ Base, Assembled, Final []string }
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(out, &source); e != nil || len(source.Profiles) != len(profiles) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v rows=%d blocked=%v %.2000s", e, len(source.Profiles), source.Capabilities.Blocked, out)
	}
	for i, row := range profiles {
		denied := map[string]bool{}
		if deny, ok := row["deny"].([]string); ok {
			for _, name := range deny {
				denied[name] = true
			}
		}
		defs := []toolschema.Definition{}
		tools := []queryloop.RegisteredTool{}
		for _, name := range []string{"Bash", "Read", "Edit"} {
			if !denied[name] {
				defs = append(defs, toolschema.Definition{Name: name})
				tools = append(tools, queryloop.RegisteredTool{Name: name})
			}
		}
		if mcp, ok := row["mcp"].([]map[string]any); ok {
			for _, decl := range mcp {
				name := decl["name"].(string)
				defs = append(defs, toolschema.Definition{Name: name})
				tools = append(tools, queryloop.RegisteredTool{Name: name, IsMCP: true})
			}
		}
		_, actual, e := mergeNativeToolPool(defs, tools)
		if e != nil {
			t.Fatal(e)
		}
		names := []string{}
		for _, tool := range actual {
			names = append(names, tool.Name)
		}
		if !reflect.DeepEqual(names, source.Profiles[i].Final) {
			t.Errorf("final pool%d Go=%v Source=%v lower=%v", i, names, source.Profiles[i].Final, source.Profiles[i].Base)
		}
	}
	if !reflect.DeepEqual(source.Profiles[0].Base, []string{"Bash", "Read", "Edit"}) || !reflect.DeepEqual(source.Profiles[0].Final, []string{"Bash", "Edit", "Read"}) {
		t.Fatal("Source lower/final stage not independently observed", source.Profiles[0])
	}
}
