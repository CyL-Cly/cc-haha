package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func presetChildFixture(t *testing.T, parent *MainPrint, cwd string) *MainPrint {
	t.Helper()
	var prepared nativeWorkflowAgentPrepared
	e := parent.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var e error
		prepared, e = parent.prepareNativeWorkflowAgent(context.Background(), scope, workflow.AgentExecution{Prompt: "Actual preset", Options: protocol.NewJSONObject(), RunID: "wf_preset-fixture", Index: 1, OnAgentID: func(string) error { return nil }, OnProgress: func(workflow.AgentProgress) error { return nil }})
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(prepared.ref.DeliveryFinished)
	if cwd != "" {
		prepared.options.Native = parent.options.Native.agentScopedBoundary(cwd)
	}
	child, e := NewMainPrint(prepared.options)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := child.ShutdownSession(context.Background()); e != nil {
			t.Error(e)
		}
	})
	if e = child.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	return child
}

func agentPresetSource(t *testing.T, parent *MainPrint, input any) struct{ SkillRows, MCPRows []json.RawMessage } {
	t.Helper()
	var result struct{ SkillRows, MCPRows []json.RawMessage }
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	n := parent.options.Native
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-preset.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = n.Home
	cmd.Env = []string{"HOME=" + n.Home, "USERPROFILE=" + n.Home, "CLAUDE_CONFIG_DIR=" + n.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(n.Home), "CC_HAHA_NATIVE_AGENT_PRESET_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=private-preset-oracle", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Source %v %.4000s", e, stderr.String())
	}
	var report struct {
		SkillRows, MCPRows []json.RawMessage
		Capabilities       struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &report); e != nil || len(report.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v blocked=%v %.3000s", e, report.Capabilities.Blocked, output)
	}
	result.SkillRows, result.MCPRows = report.SkillRows, report.MCPRows
	return result
}

func TestNativeAgentPresetSkillPromiseAllResolutionAndParentContextMatchActualSource(t *testing.T) {
	parent := backgroundAgentMain(t, "http://127.0.0.1:1")
	child := presetChildFixture(t, parent, "")
	definitions := []map[string]any{{"name": "direct", "userFacingName": "display", "aliases": []string{"alias"}}, {"name": "plug:nested"}, {"name": "other:nested"}, {"name": "failure", "fail": "Actual skill rejected"}}
	cases := []map[string]any{
		{"agentType": "plug:worker", "names": []string{"direct", "nested"}, "definitions": definitions, "expectedStarts": 2},
		{"agentType": "unrelated:worker", "names": []string{"display", "alias", "nested", "missing"}, "definitions": definitions, "expectedStarts": 3},
		{"agentType": "plug:worker", "names": []string{"failure", "direct"}, "definitions": definitions, "expectedStarts": 2},
		{"agentType": "plug:worker", "names": []string{"missing"}, "definitions": definitions, "expectedStarts": 0},
	}
	source := agentPresetSource(t, parent, map[string]any{"homeDir": parent.options.Native.Home, "skills": cases})
	if len(source.SkillRows) != len(cases) {
		t.Fatal("missing Source observations")
	}
	for i, row := range cases {
		var actual json.RawMessage
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		e := child.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			started := []string{}
			barrier := make(chan struct{})
			expected := row["expectedStarts"].(int)
			defs := []skillcommands.Definition{}
			for _, meta := range definitions {
				meta := meta
				fields := protocol.NewJSONObject()
				_ = fields.UnmarshalJSON(rawMain(meta))
				fields.Set("type", quoteMain("prompt"))
				d, e := skillcommands.NewNativeCommand(fields, func(ctx context.Context, _ *string, inv skillcommands.Invocation) (string, error) {
					if !inv.NativeScope.BelongsTo(parent.Owner) {
						return "", errors.New("preloaded skill did not receive the actual parent Owner")
					}
					started = append(started, fields.String("name"))
					if len(started) == expected {
						close(barrier)
					}
					future, e := nativeSkillWork(parent, inv.NativeScope, func() (string, error) {
						select {
						case <-barrier:
						case <-ctx.Done():
							return "", context.Cause(ctx)
						}
						if failure, ok := meta["fail"].(string); ok {
							return "", errors.New(failure)
						}
						return fields.String("name"), nil
					})
					if e != nil {
						return "", e
					}
					text, e := future.Await(inv.NativeScope)
					if e != nil {
						return "", e
					}
					permission, e := parent.nativeToolPermissionContext(inv.NativeScope)
					if e != nil {
						return "", e
					}
					model, e := parent.Host.InferenceModel(inv.NativeScope)
					return text + "|" + permission.String("mode") + "|" + model, e
				})
				if e != nil {
					return e
				}
				defs = append(defs, d)
			}
			preset := child.options.DetachedAgent.preset
			preset.definition.Name = row["agentType"].(string)
			messages, failure := preset.loadSkills(ctx, child, scope, row["names"].([]string), defs)
			projected := []protocol.JSONObject{}
			for _, m := range messages {
				projected = append(projected, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: m.Get("type")}, protocol.JSONField{Name: "message", Value: m.Get("message")}, protocol.JSONField{Name: "isMeta", Value: m.Get("isMeta")}))
			}
			var failureText any
			if failure != nil {
				failureText = failure.Error()
			}
			actual = rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "messages", Value: rawMain(projected)}, protocol.JSONField{Name: "failure", Value: rawMain(failureText)}, protocol.JSONField{Name: "started", Value: rawMain(started)}))
			return nil
		})
		cancel()
		if e != nil {
			t.Fatal(e)
		}
		if !planSourcesEqual(actual, source.SkillRows[i]) {
			t.Errorf("case%d Go%s Source%s", i, actual, source.SkillRows[i])
		}
	}
}

func TestNativeAgentPresetShellUsesParentPermissionAndWorkerCWD(t *testing.T) {
	parent := backgroundAgentMain(t, "http://127.0.0.1:1")
	cwd := filepath.Join(parent.options.Native.Home, "actual-worker-cwd")
	if e := os.MkdirAll(cwd, 0700); e != nil {
		t.Fatal(e)
	}
	child := presetChildFixture(t, parent, cwd)
	var calls atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e := child.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		d, e := skillcommands.Parse(ctx, "---\ndescription: Parent permission and child cwd\n---\n!`pwd`", "where", "projectSettings", "", "skills", nil)
		if e != nil {
			return e
		}
		text, e := child.options.DetachedAgent.preset.skillPrompt(ctx, child, scope, d)
		calls.Add(1)
		if e != nil {
			return e
		}
		// Child mode is acceptEdits, which would ask for this unapproved Bash.
		// Parent's actual bypass mode permits it; the bytes come from child pwd.
		physical, e := filepath.EvalSymlinks(cwd)
		if e != nil {
			return e
		}
		if text != physical && text != physical+"\n" {
			return fmt.Errorf("worker cwd lost: %q expected actual %q", text, physical)
		}
		return nil
	})
	if e != nil || calls.Load() != 1 {
		t.Fatal(e, calls.Load())
	}
}

func TestNativeAgentPresetMCPSelectionPolicyAndCleanupMatchActualSource(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var in protocol.JSONObject
		data, _ := io.ReadAll(r.Body)
		_ = in.UnmarshalJSON(data)
		if in.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		if r.URL.Path == "/failed" {
			w.WriteHeader(500)
			return
		}
		result := json.RawMessage(`{}`)
		switch in.String("method") {
		case "initialize":
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"actual-preset","version":"1"}}`)
		case "tools/list":
			result = []byte(`{"tools":[{"name":"echo","inputSchema":{"type":"object"}}]}`)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: in.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	defer peer.Close()
	parent := backgroundAgentMain(t, "http://127.0.0.1:1")
	// getMcpConfigByName consumes the Source schema-validated descriptor. The
	// inline branch below separately exercises its actual unknown-field spread.
	configs := map[string]any{"named": protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("http")}, protocol.JSONField{Name: "url", Value: quoteMain(peer.URL + "/named")}, protocol.JSONField{Name: "scope", Value: quoteMain("project")})}
	if e := os.WriteFile(filepath.Join(parent.options.Native.CWD, ".mcp.json"), rawMain(map[string]any{"mcpServers": configs}), 0600); e != nil {
		t.Fatal(e)
	}
	inline := map[string]any{"inline": map[string]any{"type": "http", "url": peer.URL + "/inline", "future": "keep"}}
	failed := map[string]any{"failed": map[string]any{"type": "http", "url": peer.URL + "/failed"}}
	cases := []map[string]any{{"source": "projectSettings", "specs": []any{}}, {"source": "projectSettings", "specs": []any{"missing", "named", inline, failed, map[string]any{"a": map[string]any{}, "b": map[string]any{}}}, "statuses": map[string]string{"failed": "failed"}}}
	for _, policy := range []any{true, []string{"mcp"}, []string{"hooks"}, false} {
		for _, source := range []string{"projectSettings", "userSettings", "flagSettings", "plugin", "policySettings", "built-in", "builtin", "bundled"} {
			cases = append(cases, map[string]any{"source": source, "policy": policy, "specs": []any{"named", inline}})
		}
	}
	for _, row := range cases {
		row["configs"] = configs
	}
	source := agentPresetSource(t, parent, map[string]any{"homeDir": parent.options.Native.Home, "cwd": parent.options.Native.CWD, "mcp": cases})
	if len(source.MCPRows) != len(cases) {
		t.Fatal("missing Source matrix")
	}
	child := presetChildFixture(t, parent, "")
	for i, row := range cases {
		var actual json.RawMessage
		e := child.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			preset := child.options.DetachedAgent.preset
			preset.definition.Source = row["source"].(string)
			preset.definition.Fields.Set("mcpServers", rawMain(row["specs"]))
			child.options.Native.ResolvePolicy = func() (*protocol.JSONObject, error) {
				policy := protocol.NewJSONObject(protocol.JSONField{Name: "strictPluginOnlyCustomization", Value: rawMain(row["policy"])})
				return &policy, nil
			}
			peers, inline, failure := preset.connectMCP(context.Background(), child, scope)
			calls := []protocol.JSONObject{}
			clients := []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain("parent")}, protocol.JSONField{Name: "status", Value: quoteMain("connected")})}
			tools := []string{}
			cleaned := []string{}
			for _, p := range peers {
				calls = append(calls, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(p.Name)}, protocol.JSONField{Name: "config", Value: rawMain(p.Config)}))
				clients = append(clients, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(p.Name)}, protocol.JSONField{Name: "status", Value: quoteMain(p.Status)}))
				if p.Status == "connected" {
					for _, tool := range p.Tools {
						tools = append(tools, "mcp__"+p.Name+"__"+tool.String("name"))
					}
				}
			}
			for _, p := range inline {
				if p.Status == "connected" {
					cleaned = append(cleaned, p.Name)
				}
				if e := child.mcpRuntime.CloseConnection(context.Background(), p); e != nil {
					return e
				}
			}
			var errorText any
			if failure != nil {
				errorText = failure.Error()
				clients = []protocol.JSONObject{}
			}
			actual = rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "calls", Value: rawMain(calls)}, protocol.JSONField{Name: "clients", Value: rawMain(clients)}, protocol.JSONField{Name: "tools", Value: rawMain(tools)}, protocol.JSONField{Name: "cleaned", Value: rawMain(cleaned)}, protocol.JSONField{Name: "failure", Value: rawMain(errorText)}))
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		if !planSourcesEqual(actual, source.MCPRows[i]) {
			t.Errorf("MCP case%d Go%.3500s Source%.3500s", i, actual, source.MCPRows[i])
		}
	}
}
