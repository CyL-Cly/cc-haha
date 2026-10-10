package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentResumeMetadataProjectionAndInactiveReplacementsMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	m := taskMainFixture(t)
	home := m.options.Native.Home
	_ = os.MkdirAll(m.options.Native.Temp, 0700)
	// Keep Source's early project-root memo within the disposable fixture.
	_ = os.MkdirAll(filepath.Join(home, ".git"), 0700)
	rows := []string{
		`{"runtimeAgentType":"general-purpose"}`,
		`{"runtimeAgentType":"general-purpose","persistedAgentType":"  old-agent  "}`,
		`{"runtimeAgentType":"general-purpose","persistedAgentType":" \ufeff\u2000 ","description":""}`,
		`{"runtimeAgentType":"general-purpose","persistedAgentType":null,"description":null,"toolUseId":null}`,
		`{"runtimeAgentType":"general-purpose","persistedAgentType":"\ufeffold-worker\u3000","description":" ","model":"sonnet","toolUseId":"original-card","ownerAgentId":"original-parent"}`,
		`{"runtimeAgentType":"Explore","persistedAgentType":"removed-custom-agent","description":"original description","model":"haiku"}`,
	}
	cases := []protocol.JSONObject{}
	for _, row := range rows {
		cases = append(cases, taskObject(t, []byte(row)))
	}
	tree := filepath.Join(home, "retained-worktree")
	cases[5].Set("worktreePath", quoteMain(tree))
	records := []json.RawMessage{json.RawMessage(`[]`), json.RawMessage(`[{"toolUseId":"old","replacement":"old content","mustReapply":true}]`), json.RawMessage(`[{"future":{"unknown":true},"raw":"\ud800"}]`)}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-continuation.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + m.options.Native.Temp, "CLAUDE_CODE_TMPDIR=" + m.options.Native.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_AGENT_CONTINUATION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": m.options.Native.CWD, "sessionId": m.Controller.SessionID(), "metadata": cases, "replacementRecords": records})
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.5000s", e, out)
	}
	var source struct {
		Metadata             []protocol.JSONObject
		InactiveReplacements []json.RawMessage
		Capabilities         struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Metadata) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.5000s", e, out)
	}
	for i, row := range cases {
		req := builtintools.AgentRequest{ResumeAgentID: "a0123456789abcdef", AgentType: row.String("runtimeAgentType"), PersistedAgentType: row.String("persistedAgentType"), Model: row.String("model"), PersistedDescription: row.Get("description"), ToolUseID: row.Get("toolUseId"), OwnerAgentID: row.String("ownerAgentId")}
		run := &nativeAgentWorktree{}
		if value := row.String("worktreePath"); value != "" {
			run.CWD = value
			run.Info = &worktree.AgentWorktree{Path: value, HookBased: true}
		}
		got := nativeAgentMetadata(req, run, "", false)
		if string(protocol.StringifyValidatedJavaScriptJSON(rawMain(got))) != string(protocol.StringifyValidatedJavaScriptJSON(rawMain(source.Metadata[i]))) {
			t.Errorf("case%d Go%s Source%s", i, rawMain(got), rawMain(source.Metadata[i]))
		}
	}
	for _, row := range source.InactiveReplacements {
		if string(row) != "null" {
			t.Fatal("inactive Source replacement state was not absent", string(row))
		}
	}
	// Validate the request producer also trims/falls back while keeping the UI
	// description separate from persisted metadata and using its real catalogue.
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		for i, row := range cases {
			metadata := protocol.NewJSONObject(protocol.JSONField{Name: "agentType", Value: row.Get("persistedAgentType")})
			if row.Get("description") != nil {
				metadata.Set("description", row.Get("description"))
			}
			request, e := m.nativeTools.PrepareAgentResume(scope, "real continuation", metadata)
			if e != nil {
				return e
			}
			if i == 0 && (request.Description != "(resumed)" || request.PersistedDescription != nil) {
				t.Fatal("UI fallback was persisted", request)
			}
			if expected := source.Metadata[i].String("agentType"); request.PersistedAgentType != expected && i != 5 {
				t.Errorf("actual request persisted type%d %q/%q", i, request.PersistedAgentType, expected)
			}
			if len(request.ParentTools) == 0 {
				t.Fatal("resume activity producer lost actual parent catalogue")
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
