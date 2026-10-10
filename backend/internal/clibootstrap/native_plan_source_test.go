package clibootstrap

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// Only the Source app-state consumer boundary differs here: Go uses the actual
// MainPrint controller. Both implementations read/write their native plan file.
func TestActualPlanToolCallsNormalizationResultsAndFiles(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	rows := []struct {
		name, tool, mode, disk, supplied, updated string
		hasDisk, interview                        bool
	}{
		{name: "enter", tool: "EnterPlanMode", mode: "default"},
		{name: "enter interview", tool: "EnterPlanMode", mode: "acceptEdits", interview: true},
		{name: "missing", tool: "ExitPlanMode", mode: "plan"},
		{name: "disk", tool: "ExitPlanMode", mode: "plan", disk: "physical original", hasDisk: true},
		{name: "empty", tool: "ExitPlanMode", mode: "plan", disk: "", hasDisk: true},
		{name: "JS whitespace", tool: "ExitPlanMode", mode: "plan", disk: " \ufeff\n", hasDisk: true},
		{name: "nonJS whitespace", tool: "ExitPlanMode", mode: "plan", disk: "\u0085", hasDisk: true},
		{name: "edited", tool: "ExitPlanMode", mode: "plan", disk: "original", hasDisk: true, updated: `{"plan":"user reviewed","future":true}`},
		{name: "edited empty", tool: "ExitPlanMode", mode: "plan", disk: "original", hasDisk: true, updated: `{"plan":""}`},
		{name: "edited UTF16", tool: "ExitPlanMode", mode: "plan", disk: "original", hasDisk: true, updated: `{"plan":"user \ud800"}`},
		{name: "full replacement disk fallback", tool: "ExitPlanMode", mode: "plan", disk: "original", hasDisk: true, supplied: `{"future":true}`, updated: `{}`},
		{name: "nonstring disk fallback", tool: "ExitPlanMode", mode: "plan", disk: "original", hasDisk: true, updated: `{"plan":null,"planFilePath":"ignored"}`},
		{name: "missing retains supplied", tool: "ExitPlanMode", mode: "plan", supplied: `{"plan":"supplied","future":{"keep":true}}`},
		{name: "outside plan", tool: "ExitPlanMode", mode: "default"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			f, _, options := computerUseMainFixture(t)
			delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
			f.n.Environment["CLAUDE_CODE_PLAN_MODE_INTERVIEW_PHASE"] = "false"
			if row.interview {
				f.n.Environment["CLAUDE_CODE_PLAN_MODE_INTERVIEW_PHASE"] = "true"
			}
			disabled := false
			m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual shared plan source fixture", Tools: []string{"EnterPlanMode,ExitPlanMode,Read"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 4, PermissionMode: row.mode, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
			if e := m.StartSession(context.Background()); e != nil {
				t.Fatal(e)
			}
			in := row.supplied
			if in == "" {
				in = `{}`
			}
			call := map[string]any{"tool": row.tool, "mode": row.mode, "sessionId": m.Controller.SessionID(), "slug": "actual-plan-case", "input": in, "interview": row.interview}
			if row.hasDisk {
				call["disk"] = row.disk
			}
			if row.updated != "" {
				call["updatedInput"] = json.RawMessage(row.updated)
			}
			data, _ := json.Marshal(map[string]any{"homeDir": f.home, "inputs": []any{}, "calls": []any{call}})
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plan.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
			cmd.Dir = f.home
			cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_PLAN_FIXTURE=1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(data)
			output, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("Source %v %.3000s", e, output)
			}
			var source struct {
				Calls []struct {
					Result, Mapped, Normalized, Permission, Validation json.RawMessage
					Mode                                               string
					PrePlanMode                                        *string
					HasExited, NeedsExit                               bool
					Disk                                               *string
				}
				Capabilities struct{ Blocked []string }
			}
			if e := json.Unmarshal(output, &source); e != nil || len(source.Calls) != 1 || len(source.Capabilities.Blocked) > 0 {
				t.Fatalf("Source observations %v %.3000s", e, output)
			}
			want := source.Calls[0]
			if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				cache, e := m.state.PlanSlugCache(scope)
				if e != nil {
					return e
				}
				if e := cache.Set(scope, m.Controller.SessionID(), "actual-plan-case"); e != nil {
					return e
				}
				path, e := m.plans.Path(scope, "")
				if e != nil {
					return e
				}
				if row.hasDisk {
					if e := os.WriteFile(path, []byte(row.disk), 0600); e != nil {
						return e
					}
				} else if e := os.Remove(path); e != nil && !os.IsNotExist(e) {
					return e
				}
				_, registered, e := m.nativeTools.Catalog(scope)
				if e != nil {
					return e
				}
				var tool queryloop.RegisteredTool
				for _, candidate := range registered {
					if candidate.Name == row.tool {
						tool = candidate
						break
					}
				}
				if tool.Name == "" || tool.StartCallJSON == nil || !tool.IsConcurrencySafe(protocol.NewJSONObject()) {
					return fmt.Errorf("real concurrency-safe tool missing")
				}
				var rawInput protocol.JSONObject
				_ = rawInput.UnmarshalJSON([]byte(in))
				parsed, e := tool.ParseInput(rawInput)
				if e != nil {
					return e
				}
				callContext := queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}}
				if e := tool.ValidateInput(ctx, parsed, callContext.Opaque); e != nil {
					var validation protocol.JSONObject
					_ = validation.UnmarshalJSON(want.Validation)
					if validation.String("message") != e.Error() {
						return fmt.Errorf("Source validation differs: %v %v", e, validation)
					}
					return nil
				}
				if want.Validation != nil {
					return fmt.Errorf("Go admitted Source-rejected tool")
				}
				if tool.NormalizeInputJSON != nil {
					parsed, e = tool.NormalizeInputJSON(ctx, parsed, callContext)
					if e != nil {
						return e
					}
				}
				permission, e := m.nativeTools.PermissionDecision(scope, row.tool, &parsed)
				if e != nil {
					return e
				}
				var originalPermission protocol.JSONObject
				_ = originalPermission.UnmarshalJSON(want.Permission)
				if permission.Behavior != originalPermission.String("behavior") || !planSourcesEqual(permission.Message, originalPermission.Get("message")) {
					return fmt.Errorf("permission differs Go=%s %s Source=%s", permission.Behavior, permission.Message, want.Permission)
				}
				if row.updated != "" {
					_ = parsed.UnmarshalJSON([]byte(row.updated))
				}
				if !planSourcesEqual(marshalPlan(parsed), want.Normalized) {
					return fmt.Errorf("normalized Go=%s Source=%s", marshalPlan(parsed), want.Normalized)
				}
				future, e := tool.StartCallJSON(ctx, parsed, callContext)
				if e != nil {
					return e
				}
				result, e := future.Await(scope)
				if e != nil {
					return e
				}
				if !planSourcesEqual(result.Data, want.Result) {
					return fmt.Errorf("actual call Go=%s Source=%s", result.Data, want.Result)
				}
				mapped, e := tool.MapResultJSON(result.Data, quoteMain("plan-tool-id"))
				if e != nil {
					return e
				}
				if !planSourcesEqual(marshalPlan(mapped), want.Mapped) {
					return fmt.Errorf("map Go=%s Source=%s", marshalPlan(mapped), want.Mapped)
				}
				actual, e := os.ReadFile(path)
				if want.Disk == nil {
					if !os.IsNotExist(e) {
						return fmt.Errorf("Source missing plan unexpectedly written: %v", e)
					}
				} else if e != nil || hex.EncodeToString(actual) != *want.Disk {
					return fmt.Errorf("actual plan bytes differ: %q %v %s", string(actual), e, *want.Disk)
				}
				permissionContext, e := m.Controller.PlanModeContext(scope)
				if e != nil {
					return e
				}
				if permissionContext.String("mode") != want.Mode {
					return fmt.Errorf("same Controller mode differs: %v %s", permissionContext, want.Mode)
				}
				if want.PrePlanMode == nil {
					if permissionContext.Get("prePlanMode") != nil {
						return fmt.Errorf("Source omitted prePlanMode survived: %v", permissionContext)
					}
				} else if permissionContext.String("prePlanMode") != *want.PrePlanMode {
					return fmt.Errorf("Source prePlanMode differs: %v %s", permissionContext, *want.PrePlanMode)
				}
				hasExited, needsExit, e := m.state.PlanModeFlags(scope)
				if e != nil {
					return e
				}
				if hasExited != want.HasExited || needsExit != want.NeedsExit {
					return fmt.Errorf("same Bootstrap flags differ: %v %v %v %v", hasExited, needsExit, want.HasExited, want.NeedsExit)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func marshalPlan(v protocol.JSONObject) json.RawMessage { data, _ := v.MarshalJSON(); return data }
func planSourcesEqual(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	return reflect.DeepEqual(protocol.StringifyValidatedJavaScriptJSON(a), protocol.StringifyValidatedJavaScriptJSON(b))
}
