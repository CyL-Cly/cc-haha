package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

func TestActualInlineSkillCallsMessagesModifiersAndValidationMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	files := map[string]string{
		"plain":     "---\ndescription: Ordinary prompt\n---\nUse $ARGUMENTS; do not interpret email@local.",
		"grant":     "---\ndescription: Additional grants\nallowed-tools: [Read Write, 'Skill(other)', Read]\n---\nExecute additional tools.",
		"model":     "---\ndescription: Model override\nmodel: sonnet\neffort: low\n---\nUse the skill model.",
		"modelonly": "---\ndescription: Model only\nuser-invocable: false\n---\nOnly called by the model.",
		"disabled":  "---\ndescription: Cannot invoke\ndisable-model-invocation: true\n---\nDo not call.",
	}
	for name, body := range files {
		f.write(t, filepath.Join(f.cwd, ".claude", "skills", name, "SKILL.md"), body)
	}
	inputs := []string{`{"skill":" /plain ","args":"one two"}`, `{"skill":"grant"}`, `{"skill":"model"}`, `{"skill":"modelonly","args":"hidden"}`, `{"skill":"disabled"}`, `{"skill":"unknown"}`, `{"skill":" / "}`, `{"skill":"\ufeff \t "}`}
	rows := []map[string]string{}
	for _, input := range inputs {
		rows = append(rows, map[string]string{"input": input})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-native-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": f.home, "cwd": f.cwd, "calls": rows})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		Calls        []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.Calls) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v rows=%d blocked=%v %.1000s", e, len(source.Calls), source.Capabilities.Blocked, output)
	}
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "no model in original inline Skill contract test", Tools: []string{"Skill,Read,Write"}, PermissionMode: "default", MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	for i, input := range inputs {
		if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			var in protocol.JSONObject
			_ = in.UnmarshalJSON([]byte(input))
			_, catalog, e := m.nativeTools.Catalog(scope)
			if e != nil {
				return e
			}
			var skill queryloop.RegisteredTool
			for _, tool := range catalog {
				if tool.Name == "Skill" {
					skill = tool
				}
			}
			parsed, e := skill.ParseInput(in)
			if e != nil {
				return e
			}
			actualContext := &builtinwrite.CallContext{Scope: scope, MainLoopModel: "opus[1m]"}
			validation := protocol.NewJSONObject(protocol.JSONField{Name: "result", Value: []byte("true")})
			if e = skill.ValidateInput(ctx, parsed, actualContext); e != nil {
				var failure *builtintools.SkillValidationError
				if !errors.As(e, &failure) {
					return e
				}
				validation.Set("result", []byte("false"))
				validation.Set("message", quoteMain(failure.Message))
				validation.Set("errorCode", rawMain(failure.ErrorCode))
			}
			actual := protocol.NewJSONObject(protocol.JSONField{Name: "validation", Value: rawMain(validation)})
			if string(validation.Get("result")) == "true" {
				var blocks []protocol.JSONObject
				_ = json.Unmarshal([]byte(`[{"type":"tool_use","name":"Skill","id":"first-skill-id","input":{}},{"type":"tool_use","name":"Skill","id":"second-skill-id","input":{}}]`), &blocks)
				parent := streamhistory.Envelope{}
				for _, block := range blocks {
					parent.Content = append(parent.Content, &streamhistory.Block{Object: block})
				}
				result, e := skill.CallJSON(ctx, parsed, queryloop.JSONToolCallContext{Opaque: actualContext, Invocation: queryloop.Invocation{Assistant: &parent}})
				if e != nil {
					return e
				}
				projected := []protocol.JSONObject{}
				for _, message := range result.NewMessages {
					record := message.Record.Clone()
					record.Delete("uuid")
					record.Delete("timestamp")
					projected = append(projected, record)
				}
				modified, e := result.ModifyContext(actualContext)
				if e != nil {
					return e
				}
				state := modified.(*builtinwrite.CallContext)
				model, effort, rules := quoteMain("opus[1m]"), quoteMain("high"), json.RawMessage("[]")
				if state.SkillOverrides != nil {
					if raw := state.SkillOverrides.Get("model"); raw != nil {
						model = raw
					}
					if raw := state.SkillOverrides.Get("effort"); raw != nil {
						effort = raw
					}
					if raw := state.SkillOverrides.Get("commandRules"); raw != nil {
						rules = raw
					}
				}
				actual.Set("data", result.Data)
				actual.Set("messages", rawMain(projected))
				actual.Set("model", model)
				actual.Set("effort", effort)
				actual.Set("commandRules", rules)
			}
			if !planSourcesEqual(rawMain(actual), rawMain(source.Calls[i])) {
				return fmt.Errorf("call%d Go=%.2000s Source=%.2000s", i, rawMain(actual), rawMain(source.Calls[i]))
			}
			return nil
		}); e != nil {
			t.Error(e)
		}
	}
}
