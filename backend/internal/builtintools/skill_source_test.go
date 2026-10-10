package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func TestSkillPermissionPriorityAndCommandMetadataMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	_ = os.MkdirAll(filepath.Join(home, ".git"), 0700)
	_ = os.WriteFile(filepath.Join(home, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0600)
	base := `{"name":"zzfixture","type":"prompt","description":"Fixture","loadedFrom":"mcp","source":"mcp"}`
	unsafe := `{"name":"zzfixture","type":"prompt","description":"Fixture","loadedFrom":"mcp","source":"mcp","allowedTools":["Write"]}`
	rows := []map[string]any{}
	add := func(command, p string) {
		rows = append(rows, map[string]any{"command": command, "input": `{"skill":" /zzfixture ","args":"actual"}`, "permission": json.RawMessage(p)})
	}
	add(base, `{}`)
	add(unsafe, `{}`)
	for _, command := range []string{base, unsafe} {
		for _, p := range []string{
			`{"mode":"bypassPermissions"}`, `{"mode":"plan","isBypassPermissionsModeAvailable":true}`,
			`{"alwaysDenyRules":{"projectSettings":["Skill(*)"]}}`, `{"alwaysDenyRules":{"projectSettings":["Skill()"]}}`,
			`{"alwaysAskRules":{"userSettings":["Skill"]},"alwaysAllowRules":{"session":["Skill(zzfixture)"]}}`,
			`{"mode":"bypassPermissions","alwaysAskRules":{"userSettings":["Skill"]}}`,
			`{"mode":"bypassPermissions","alwaysDenyRules":{"session":["Skill(zzfixture)"]}}`,
			`{"alwaysAllowRules":{"projectSettings":["Skill()"]}}`,
			`{"alwaysAllowRules":{"userSettings":["Skill(zzfixture)"],"projectSettings":["Skill(zzfixture)"]}}`,
			`{"alwaysAllowRules":{"userSettings":["Skill(zzfixture)"]},"alwaysDenyRules":{"session":["Skill(/zzfixture)"]}}`,
			`{"alwaysAllowRules":{"cliArg":["Skill(/zz:*)"]}}`,
			`{"alwaysAskRules":{"userSettings":["Skill(zzfixture)"]}}`,
			`{"alwaysAllowRules":{"userSettings":["Skill(zzfixture)tail","Skill(zzfixture\\))","Skill(zzfixture\\\\)"]}}`,
		} {
			add(command, p)
		}
	}
	for _, value := range []string{`null`, `{}`, `[]`, `false`, `0`, `""`, `[false]`, `{"x":1}`} {
		var command protocol.JSONObject
		_ = command.UnmarshalJSON([]byte(base))
		command.Set("future", []byte(value))
		add(string(raw(command)), `{}`)
	}
	// Actual Source Skill permission matching, including two logical names
	// that have the same lossy display string, aliases and all rule priorities.
	for _, name := range []string{`same-\ud800:reader`, `same-\udc00:reader`} {
		for _, permission := range []string{
			`{}`, `{"mode":"bypassPermissions"}`, `{"mode":"plan","isBypassPermissionsModeAvailable":true}`,
			`{"alwaysDenyRules":{"session":["Skill(same-\ud800:reader)"]}}`,
			`{"alwaysDenyRules":{"session":["Skill(/same-\udc00:reader)"]}}`,
			`{"alwaysDenyRules":{"session":["Skill(same-\ud800:*)"]}}`,
			`{"alwaysAllowRules":{"userSettings":["Skill(same-\ud800:reader)"],"projectSettings":["Skill(same-\ud800:reader)"]}}`,
			`{"alwaysAllowRules":{"session":["Skill(/same-\udc00:*)"]}}`,
			`{"alwaysDenyRules":{"session":["Skill(*)"]}}`,
			`{"alwaysAskRules":{"session":["Skill"]}}`,
			`{"mode":"bypassPermissions","alwaysDenyRules":{"session":["Skill(same-\ud800:reader)"]},"alwaysAllowRules":{"projectSettings":["Skill"]}}`,
			`{"alwaysAskRules":{"session":["Skill(same-\ud800:reader)"]}}`,
		} {
			command := `{"name":"` + name + `","type":"prompt","description":"Fixture","loadedFrom":"mcp","source":"mcp","allowedTools":["Write"]}`
			rows = append(rows, map[string]any{"command": command, "input": `{"skill":" \ufeff/` + name + `\u3000 ","args":"actual"}`, "permission": json.RawMessage(permission)})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "permissions": rows})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		Permissions  []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.Permissions) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v rows=%d blocked=%v %.1000s", e, len(source.Permissions), source.Capabilities.Blocked, output)
	}
	s, owner, _, permission := fixtureServiceForSkill(t)
	for i, row := range rows {
		if e := owner.Run(ctx, func(scope *cooperative.Scope) error {
			var command protocol.JSONObject
			_ = command.UnmarshalJSON([]byte(row["command"].(string)))
			s.options.Skill = &SkillBindings{Commands: func(*cooperative.Scope) ([]SkillCommand, error) { return []SkillCommand{{Metadata: command}}, nil }, Call: func(context.Context, *cooperative.Scope, string, SkillCommand, *string, queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
				return queryloop.ToolCallResult{}, fmt.Errorf("unexpected actual execution")
			}}
			var c protocol.JSONObject
			_ = c.UnmarshalJSON(row["permission"].(json.RawMessage))
			*permission = c
			in := input(row["input"].(string))
			result, e := s.skillPermission(scope, &in)
			if e != nil {
				return e
			}
			actual := protocol.NewJSONObject(field("behavior", result.Behavior))
			if result.UpdatedInput != nil {
				actual.Set("updatedInput", raw(*result.UpdatedInput))
			}
			actual.Set("message", result.Message)
			actual.Set("suggestions", result.Suggestions)
			actual.Set("decisionReason", result.DecisionReason)
			actual.Set("metadata", result.Metadata)
			if !skillExactJSON(raw(actual), source.Permissions[i]) {
				return fmt.Errorf("case%d Go%s Source%s", i, raw(actual), source.Permissions[i])
			}

			return nil
		}); e != nil {
			t.Error(e)
		}
	}
}

func fixtureServiceForSkill(t *testing.T) (*Service, *cooperative.Owner, string, *protocol.JSONObject) {
	return fixture(t)
}

// Compare the final JSON values without Go's replacement of lone UTF-16 units.
func skillExactJSON(a, b json.RawMessage) bool {
	if au, e := protocol.DecodeJavaScriptJSONString(a); e == nil {
		bu, e := protocol.DecodeJavaScriptJSONString(b)
		return e == nil && string(protocol.QuoteJavaScriptUTF16(au)) == string(protocol.QuoteJavaScriptUTF16(bu))
	}
	var ao, bo protocol.JSONObject
	if ao.UnmarshalJSON(a) == nil && ao.IsObject() {
		if bo.UnmarshalJSON(b) != nil || !bo.IsObject() {
			return false
		}
		ap, bp := ao.OwnProperties(), bo.OwnProperties()
		if len(ap) != len(bp) {
			return false
		}
		for _, p := range ap {
			found := false
			for _, q := range bp {
				if skillSameName(p.NameJSON, q.NameJSON) {
					found = skillExactJSON(p.Value, q.Value)
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	var aa, ba []json.RawMessage
	if json.Unmarshal(a, &aa) == nil && aa != nil {
		if json.Unmarshal(b, &ba) != nil || len(aa) != len(ba) {
			return false
		}
		for i := range aa {
			if !skillExactJSON(aa[i], ba[i]) {
				return false
			}
		}
		return true
	}
	ac, ae := protocol.ParseAndStringifyJavaScriptJSON(a)
	bc, be := protocol.ParseAndStringifyJavaScriptJSON(b)
	return ae == nil && be == nil && string(ac) == string(bc)
}
