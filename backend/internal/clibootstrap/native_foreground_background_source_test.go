package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestForegroundBackgroundSignalAndAutoDelayMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	m := taskMainFixture(t)
	if e := os.MkdirAll(m.options.Native.Temp, 0700); e != nil {
		t.Fatal(e)
	}
	home := m.options.Native.Home
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const now = 1700000000000.0
	selected := taskObject(t, []byte(`{"agentType":"general-purpose","tools":["Read"],"description":"real selected definition"}`))
	rows := []map[string]any{{"id": "a0123456789abcdef", "description": "Manual foreground", "prompt": "Source foreground prompt", "selectedAgent": selected, "toolUseId": "original-foreground-card", "now": now}, {"id": "a0123456789abcdee", "description": "Auto foreground", "prompt": "Source auto prompt", "selectedAgent": selected, "toolUseId": "original-auto-card", "autoBackgroundMs": 2, "now": now}}
	autoValues := []any{nil, "", "false", "0", "true", "TRUE", " yes ", "on", "1", "no", "\ufefftrue\ufeff", "\u0085true\u0085"}
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-continuation.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", path)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + m.options.Native.Temp, "CLAUDE_CODE_TMPDIR=" + m.options.Native.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_AGENT_CONTINUATION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "cwd": m.options.Native.CWD, "sessionId": m.Controller.SessionID(), "foregrounds": rows, "autoValues": autoValues})
	command.Stdin = bytes.NewReader(input)
	out, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, out)
	}
	var source struct {
		Foregrounds  []protocol.JSONObject
		AutoDelays   []float64
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Foregrounds) != 2 || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.3000s", e, out)
	}
	m.options.Native.NowMillis = func() float64 { return now }
	for i, row := range rows {
		if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			id := row["id"].(string)
			path, _, _, e := m.Controller.AgentTranscriptSourceOnOwner(scope, id)
			if e != nil {
				return e
			}
			request := builtintools.AgentRequest{Description: row["description"].(string), Prompt: row["prompt"].(string), AgentType: "general-purpose", SelectedAgent: selected, ToolUseID: quoteMain(row["toolUseId"].(string))}
			taskCtx, taskCancel := context.WithCancelCause(context.Background())
			defer taskCancel(nil)
			done := make(chan struct{})
			defer close(done)
			if e = m.beginForegroundAgentTask(scope, id, path, request, taskCancel, done); e != nil {
				return e
			}
			entry, e := m.nativeBackgroundBindings().Lookup(scope, id)
			if e != nil {
				return e
			}
			if !taskSemanticEqual(rawMain(entry.Data), source.Foregrounds[i].Get("initial")) {
				t.Errorf("initial%d Go=%s Source=%s", i, rawMain(entry.Data), source.Foregrounds[i].Get("initial"))
			}
			stop, e := m.bindNativeForegroundPromotion(scope, id, taskCancel)
			if e != nil {
				return e
			}
			defer stop()
			changed, e := m.Controller.BackgroundForegroundAgentsOnOwner(scope)
			if e != nil || !changed {
				return e
			}
			if context.Cause(taskCtx) != errAgentPromotedToBackground {
				t.Error("real promotion cause not delivered")
			}
			entry, e = m.nativeBackgroundBindings().Lookup(scope, id)
			if e != nil {
				return e
			}
			if !taskSemanticEqual(rawMain(entry.Data), source.Foregrounds[i].Get("promoted")) {
				t.Errorf("promoted%d Go=%s Source=%s", i, rawMain(entry.Data), source.Foregrounds[i].Get("promoted"))
			}
			again, e := m.Controller.BackgroundForegroundAgentsOnOwner(scope)
			if e != nil || again {
				t.Error("duplicate promotion changed original task")
			}
			if string(source.Foregrounds[i].Get("resolved")) != "true" || string(source.Foregrounds[i].Get("duplicate")) != "false" || string(source.Foregrounds[i].Get("retainedAfterUnregister")) != "true" {
				t.Error("Source signal contract unexpected")
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
	}
	for i, value := range autoValues {
		if value == nil {
			delete(m.options.Native.Environment, "CLAUDE_AUTO_BACKGROUND_TASKS")
		} else {
			m.options.Native.Environment["CLAUDE_AUTO_BACKGROUND_TASKS"] = value.(string)
		}
		got := float64(m.nativeAgentAutoBackgroundDelay() / time.Millisecond)
		if got != source.AutoDelays[i] {
			t.Errorf("Source auto value%v Go=%v Source=%v", value, got, source.AutoDelays[i])
		}
	}
}
