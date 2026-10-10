package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func TestMCPResourcePermissionsMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	if e := os.MkdirAll(filepath.Join(home, "tmp"), 0700); e != nil {
		t.Fatal(e)
	}
	rows := []map[string]any{}
	for _, name := range []string{"ListMcpResourcesTool", "ReadMcpResourceTool"} {
		for _, p := range []string{
			`{}`, `{"mode":"plan"}`, `{"mode":"acceptEdits"}`, `{"mode":"dontAsk"}`, `{"mode":"bypassPermissions"}`, `{"mode":"plan","isBypassPermissionsModeAvailable":true}`,
			`{"alwaysDenyRules":{"projectSettings":["TOOL"]}}`, `{"alwaysDenyRules":{"projectSettings":["TOOL(*)"]}}`, `{"alwaysDenyRules":{"projectSettings":["TOOL()"]}}`,
			`{"alwaysDenyRules":{"projectSettings":["TOOL(fixture)"]}}`, `{"alwaysAskRules":{"projectSettings":["TOOL(fixture)"]}}`,
			`{"alwaysAskRules":{"userSettings":["TOOL"]},"alwaysAllowRules":{"session":["TOOL"]}}`,
			`{"mode":"dontAsk","alwaysAskRules":{"session":["TOOL"]}}`,
			`{"mode":"bypassPermissions","alwaysAskRules":{"userSettings":["TOOL"]}}`,
			`{"mode":"bypassPermissions","alwaysDenyRules":{"session":["TOOL"]}}`,
			`{"alwaysAllowRules":{"projectSettings":["TOOL()"]}}`,
			`{"alwaysAllowRules":{"userSettings":["TOOL"],"projectSettings":["TOOL"]}}`,
			`{"alwaysDenyRules":{"userSettings":["TOOL"],"projectSettings":["TOOL"]}}`,
			`{"alwaysAllowRules":{"session":["TOOL(*)tail","TOOL(fixture\\))"]}}`,
		} {
			p = strings.ReplaceAll(p, "TOOL", name)
			in := `{ "server":"fixture", "future":true }`
			if name == "ReadMcpResourceTool" {
				in = `{"server":"fixture","uri":"scheme:actual","future":true}`
			}
			rows = append(rows, map[string]any{"name": name, "input": in, "permission": json.RawMessage(p)})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixturePath, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "resourcePermissions": rows})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		ResourcePermissions []json.RawMessage
		Capabilities        struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.ResourcePermissions) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v rows=%d blocked=%v %.1000s", e, len(source.ResourcePermissions), source.Capabilities.Blocked, output)
	}
	s, owner, _, permission := fixture(t)
	for i, row := range rows {
		if e := owner.Run(ctx, func(scope *cooperative.Scope) error {
			var c protocol.JSONObject
			_ = c.UnmarshalJSON(row["permission"].(json.RawMessage))
			*permission = c
			definition, e := toolschema.NativeBuiltinDefinition(row["name"].(string))
			if e != nil {
				return e
			}
			in, e := toolschema.ParseInput(definition, input(row["input"].(string)))
			if e != nil {
				return e
			}
			result, e := s.PermissionDecision(scope, row["name"].(string), &in)
			if e != nil {
				return e
			}
			actual := protocol.NewJSONObject(field("behavior", result.Behavior))
			if result.UpdatedInput != nil {
				actual.Set("updatedInput", raw(*result.UpdatedInput))
			}
			actual.Set("decisionReason", result.DecisionReason)
			actual.Set("message", result.Message)
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(raw(actual)), protocol.StringifyValidatedJavaScriptJSON(source.ResourcePermissions[i])) {
				return fmt.Errorf("case%d Go=%s Source=%s", i, raw(actual), source.ResourcePermissions[i])
			}
			return nil
		}); e != nil {
			t.Error(e)
		}
	}
}
