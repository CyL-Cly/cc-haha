package sdkpermissions

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourcePlanContextTransitionsAndModeRestoration(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	rows := []struct {
		Tool, Mode, Pre string
		Needs           bool
	}{{"EnterPlanMode", "default", "", true}, {"EnterPlanMode", "acceptEdits", "", true}, {"EnterPlanMode", "bypassPermissions", "", false}, {"EnterPlanMode", "plan", "default", true}, {"ExitPlanMode", "plan", "default", false}, {"ExitPlanMode", "plan", "bypassPermissions", false}, {"ExitPlanMode", "plan", "", false}}
	calls := []map[string]any{}
	for i, row := range rows {
		calls = append(calls, map[string]any{"tool": row.Tool, "mode": row.Mode, "prePlanMode": row.Pre, "needsExit": row.Needs, "sessionId": "plan-state-" + string(rune('a'+i)), "slug": "state-slug-" + string(rune('a'+i))})
	}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "inputs": []any{}, "calls": calls})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plan.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_PLAN_FIXTURE=1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(data)
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("Source: %v %.3000s", e, output)
	}
	var source struct {
		Calls []struct {
			Mode        string
			PrePlanMode *string
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil {
		t.Fatalf("Source=%s %v", output, e)
	}
	if len(source.Calls) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal("Source rows/capabilities", source)
	}
	owner := cooperative.New()
	defer owner.Close()
	for i, row := range rows {
		if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			context := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quote(row.Mode)}, protocol.JSONField{Name: "isBypassPermissionsModeAvailable", Value: json.RawMessage("true")}, protocol.JSONField{Name: "unknown", Value: json.RawMessage(`{"keep":true}`)})
			if row.Pre != "" {
				context.Set("prePlanMode", quote(row.Pre))
			}
			s := &State{owner: owner, context: context, nativeProcess: true}
			if row.Tool == "EnterPlanMode" {
				if e := s.EnterPlanMode(scope); e != nil {
					return e
				}
			} else {
				if _, e := s.ExitPlanMode(scope); e != nil {
					return e
				}
			}
			got, e := s.PlanModeContext(scope)
			if e != nil {
				return e
			}
			expected := source.Calls[i]
			if got.String("mode") != expected.Mode {
				t.Fatalf("row%d Source=%s Go=%s", i, expected.Mode, got.String("mode"))
			}
			pre := expected.PrePlanMode
			if pre == nil {
				if got.Get("prePlanMode") != nil {
					t.Fatal("Source omitted prePlanMode not cleared")
				}
			} else if got.String("prePlanMode") != *pre {
				t.Fatal("Source prePlanMode differs", got.String("prePlanMode"), *pre)
			}
			if got.Get("unknown") == nil {
				t.Fatal("mode transition dropped unknown context")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
}
func TestAutoPlanContextIsAnExplicitCapabilityWithoutMutation(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	for _, raw := range []string{`{"mode":"auto"}`, `{"mode":"plan","prePlanMode":"auto"}`, `{"mode":"plan","strippedDangerousRules":{}}`} {
		if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			var context protocol.JSONObject
			_ = context.UnmarshalJSON([]byte(raw))
			s := &State{owner: owner, context: context, nativeProcess: true}
			if e := s.EnterPlanMode(scope); e == nil {
				t.Fatal("auto policy discarded")
			}
			got, _ := s.context.MarshalJSON()
			if string(got) != raw {
				t.Fatal("unsupported plan transition mutated context", string(got))
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
}
