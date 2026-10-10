package clibootstrap

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

	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/ultracode"
)

type ultraSourceRow struct {
	Kind        string                `json:"kind"`
	Text        string                `json:"text,omitempty"`
	Args        string                `json:"args,omitempty"`
	Model       string                `json:"model,omitempty"`
	Interactive bool                  `json:"interactive"`
	Suppressed  bool                  `json:"suppressed,omitempty"`
	Active      bool                  `json:"active,omitempty"`
	History     []protocol.JSONObject `json:"history,omitempty"`
	Settings    protocol.JSONObject   `json:"settings"`
	Environment map[string]any        `json:"environment"`
}

func TestActualSourceUltracodeKeywordTransitionRendererEffortAndModelGate(t *testing.T) {
	t.Parallel()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []ultraSourceRow{}
	base := func(kind string) ultraSourceRow {
		return ultraSourceRow{Kind: kind, Interactive: true, Model: "fixture-model", Settings: protocol.NewJSONObject(protocol.JSONField{Name: "future", Value: rawMain(map[string]bool{"keep": true})}), Environment: map[string]any{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "ANTHROPIC_DEFAULT_SONNET_MODEL": "fixture-model", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES": "effort,xhigh_effort", "CLAUDE_CODE_EFFORT_LEVEL": nil, "CLAUDE_CODE_DISABLE_WORKFLOWS": nil, "CLAUDE_CODE_USE_BEDROCK": nil, "CLAUDE_CODE_USE_VERTEX": nil, "CLAUDE_CODE_USE_FOUNDRY": nil, "CLAUDE_CODE_USE_AZURE_OPENAI": nil}}
	}
	for _, text := range []string{"ultracode: audit", "please Ultracode this repo", "👋 ultracode，go", "don't stop — ultracode this", "`ultracode`", "```text\nultracode\n```", `what does the "ultracode" keyword do?`, "{ mode: ultracode }", "[ultracode]", "(ultracode)", "<ultracode>", "docs/ultracode/readme.md", "ultracode.ts", "--ultracode", "ultracode-runner", "what is ultracode?", "superultracoded", "'ultracode'", "unclosed ` ultracode", "< not a tag ultracode"} {
		r := base("keyword")
		r.Text = text
		rows = append(rows, r)
	}
	for _, profile := range []string{"noninteractive", "suppressed", "keyword-disabled", "workflow-disabled", "managed-toggle", "env-disabled"} {
		r := base("keyword")
		r.Text = "ultracode: inspect"
		switch profile {
		case "noninteractive":
			r.Interactive = false
		case "suppressed":
			r.Suppressed = true
		case "keyword-disabled":
			r.Settings.Set("workflowKeywordTriggerEnabled", rawMain(false))
		case "workflow-disabled":
			r.Settings.Set("disableWorkflows", rawMain(true))
		case "managed-toggle":
			r.Settings.Set("enableWorkflows", rawMain(false))
		case "env-disabled":
			r.Environment["CLAUDE_CODE_DISABLE_WORKFLOWS"] = "1"
		}
		rows = append(rows, r)
	}
	attachment := func(kind string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("attachment")}, protocol.JSONField{Name: "attachment", Value: rawMain(map[string]string{"type": kind})})
	}
	for _, active := range []bool{false, true} {
		for _, history := range [][]protocol.JSONObject{nil, {attachment("ultra_effort_enter")}, {attachment("ultra_effort_exit")}, {attachment("ultra_effort_enter"), attachment("ultra_effort_exit")}, {attachment("ultra_effort_exit"), attachment("workflow_keyword_request"), attachment("ultra_effort_enter")}} {
			r := base("transition")
			r.Active = active
			r.History = history
			rows = append(rows, r)
		}
	}
	for _, model := range []string{"fixture-model", "deepseek-v4-flash", "gpt-6-sol", "gpt-6-luna", "gpt-future", "grok-4.7", "grok-4.5", "claude-opus-4-7", "claude-opus-4-6"} {
		r := base("gate")
		r.Model = model
		rows = append(rows, r)
	}
	for _, env := range []map[string]any{{"ANTHROPIC_BASE_URL": "https://api.anthropic.com"}, {"ANTHROPIC_BASE_URL": "https://api.anthropic.com:443"}, {"ANTHROPIC_BASE_URL": "https://api.anthropic.com:1234"}, {"ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES": "effort"}, {"CLAUDE_CODE_USE_BEDROCK": "1"}} {
		r := base("gate")
		r.Model = "claude-opus-4-7"
		for k, v := range env {
			r.Environment[k] = v
		}
		rows = append(rows, r)
	}
	for _, baseURL := range []string{"https://api.anthropic.com", "http://127.0.0.1:1"} {
		r := base("gate")
		r.Environment["ANTHROPIC_BASE_URL"] = baseURL
		rows = append(rows, r)
	}
	for _, args := range []string{"ultracode", "ULTRACODE", "low", "medium", "high", "xhigh", "max", "auto", "unset", "invalid"} {
		r := base("effort")
		r.Args = args
		r.Settings.Set("effortLevel", quoteMain("medium"))
		rows = append(rows, r)
	}
	for _, env := range []string{"high", "xhigh", "auto", "unset", "invalid", "50", "+1", "-1", "9007199254740993"} {
		r := base("effort")
		r.Args = "ultracode"
		r.Environment["CLAUDE_CODE_EFFORT_LEVEL"] = env
		rows = append(rows, r)
	}
	for _, profile := range []string{"model", "workflows"} {
		r := base("effort")
		r.Args = "ultracode"
		if profile == "model" {
			r.Model = "deepseek-v4-flash"
		} else {
			r.Settings.Set("disableWorkflows", rawMain(true))
		}
		rows = append(rows, r)
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-ultracode.fixture.ts")
	input, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_ULTRACODE_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var out, diagnostics bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &diagnostics
	if e := cmd.Run(); e != nil {
		t.Fatal(e, out.String(), diagnostics.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(out.Bytes(), &source); e != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, out.String(), diagnostics.String())
	}
	canonical := func(raw []byte) any {
		var result any
		if e := json.Unmarshal(raw, &result); e != nil {
			t.Fatal(e, string(raw))
		}
		return result
	}
	for i, row := range rows {
		// Capability-only profiles use the original caller environment below.
		// MainPrint stays on its private peer: first-party remote policy startup
		// is a separate guarded producer, not part of this effort leaf test.
		m := ultracodeMain(t, "http://127.0.0.1:1", "", row.Interactive, nil, func(f *nativePluginFixture) {
			for k, v := range row.Environment {
				if row.Kind == "gate" {
					continue
				}
				if v == nil {
					delete(f.n.Environment, k)
				} else {
					f.n.Environment[k] = v.(string)
				}
			}
			settings := row.Settings.Clone()
			settings.Set("enabledPlugins", rawMain(map[string]bool{"sample@mkt": false}))
			f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(settings)))
		})
		var actual protocol.JSONObject
		if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			for k, v := range row.Environment {
				if row.Kind == "gate" {
					continue
				}
				if v == nil {
					delete(m.options.Native.Environment, k)
				} else {
					m.options.Native.Environment[k] = v.(string)
				}
			}
			actual = protocol.NewJSONObject()
			switch row.Kind {
			case "keyword", "transition":
				s, _ := m.ultracodeState(scope)
				s.active = row.Active
				s.suppressed = row.Suppressed
				var produced []queryloop.Message
				var e error
				if row.Kind == "keyword" {
					actual.Set("ranges", rawMain(ultracode.Ranges(row.Text)))
					produced, e = m.nativeWorkflowKeywordAttachment(scope, row.Text)
				} else {
					history := []queryloop.Message{}
					for _, record := range row.History {
						history = append(history, queryloop.Message{Record: record})
					}
					produced, e = m.nativeUltracodeAttachments(scope, history, "")
				}
				if e != nil {
					return e
				}
				payload := []json.RawMessage{}
				content := []json.RawMessage{}
				for _, message := range produced {
					raw := message.Record.Get("attachment")
					payload = append(payload, raw)
					mapped, e := attachments.Project(raw, m.factory)
					if e != nil {
						return e
					}
					for _, record := range mapped {
						api, _ := record.Object("message")
						content = append(content, api.Get("content"))
					}
				}
				actual.Set("payload", rawMain(payload))
				actual.Set("content", rawMain(content))
			case "gate":
				enabled, e := m.workflowDefinitions.EnabledOnOwner(scope)
				if e != nil {
					return e
				}
				// Test the gate with the original caller environment, before
				// native process startup projects model aliases/capabilities.
				gateEnv := map[string]string{}
				for key, value := range row.Environment {
					if value != nil {
						gateEnv[key] = value.(string)
					}
				}
				ok := enabled && sourceXHighEffort(row.Model, gateEnv)
				actual.Set("ok", rawMain(ok))
				if !ok {
					reason := "model"
					if !enabled {
						reason = "workflows-disabled"
					}
					actual.Set("reason", quoteMain(reason))
				}
			case "effort":
				if e := m.Host.SetInferenceModel(scope, row.Model, nil, true); e != nil {
					return e
				}
				text, e := m.nativeEffortCommand(scope, row.Args)
				if e != nil {
					return e
				}
				result := protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: quoteMain(text)})
				// Original refusal/invalid responses omit effortUpdate and preserve state.
				if !bytes.HasPrefix([]byte(text), []byte("Ultracode needs")) && !bytes.HasPrefix([]byte(text), []byte("Ultracode runs")) && !bytes.HasPrefix([]byte(text), []byte("Invalid argument")) {
					value, _, e := m.Host.InferenceEffort(scope)
					if e != nil {
						return e
					}
					update := protocol.NewJSONObject()
					update.Set("value", value)
					if stringsEqualUltra(row.Args) {
						update.Set("ultracode", rawMain(true))
					}
					result.Set("effortUpdate", rawMain(update))
				}
				actual.Set("result", rawMain(result))
				raw, e := os.ReadFile(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "settings.json"))
				if e != nil {
					return e
				}
				var settings protocol.JSONObject
				_ = settings.UnmarshalJSON(raw)
				settings.Delete("enabledPlugins")
				actual.Set("settings", rawMain(settings))
			}
			return nil
		}); e != nil {
			t.Fatalf("row%d %s %v", i, row.Kind, e)
		}
		if !reflect.DeepEqual(canonical(rawMain(actual)), canonical(source.Rows[i])) {
			t.Fatalf("actual Source row%d %s text=%q args=%q model=%s\nGo=%s\nSource=%s", i, row.Kind, row.Text, row.Args, row.Model, rawMain(actual), source.Rows[i])
		}
		if e := m.ShutdownSession(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
}
func stringsEqualUltra(value string) bool { return value == "ultracode" || value == "ULTRACODE" }
