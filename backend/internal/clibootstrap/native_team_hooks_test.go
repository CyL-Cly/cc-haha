package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

func TestActualTeamMainFrontmatterStopExecutesOnSessionRatherThanSubagentAndActivatesOnce(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		emitWorktreeModel(w, 1, "", "", "Actual worker turn complete")
	}))
	defer provider.Close()
	names := []string{"Read"}
	start := teamworker.Start{ParentSessionID: "11111111-1111-4111-8111-111111111111", TeamName: "original-team", MemberID: "member", Name: "worker", SystemPrompt: "Approved worker real main Stop", Tools: &names, AgentDefinition: protocol.NewJSONObject()}
	m, f, _ := teamWorkerFixture(t, start, provider.URL)
	// Main's actual shell/Hook consumer needs the normal Source path.
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	inputPath := filepath.Join(f.cwd, "actual-team-stop-input.json")
	command := "cat > '" + strings.ReplaceAll(inputPath, "'", "'\\''") + "'"
	err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		m.teamWorker.preset.Definition.Fields.Set("hooks", rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "Stop", Value: rawMain([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "hooks", Value: rawMain([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("command")}, protocol.JSONField{Name: "command", Value: quoteMain(command)})})})})})))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"Execute first released approved turn", "Continue same main worker"} {
		input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain(uuidMain())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": prompt})})
		if err = m.RunTurn(ctx, input); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(inputPath)
		if err != nil {
			t.Fatal("actual Stop process did not run", err)
		}
		var value protocol.JSONObject
		if err = value.UnmarshalJSON(data); err != nil {
			t.Fatal(err, string(data))
		}
		if value.String("hook_event_name") != "Stop" || value.String("session_id") != m.Controller.SessionID() || value.Get("agent_id") != nil {
			t.Fatal("main Stop identity was replaced by subagent", string(data))
		}
	}
	err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		declarations := m.hook.NativeMainHookDeclarations(m.Controller.SessionID())
		var matchers []protocol.JSONObject
		_ = json.Unmarshal(declarations.Get("Stop"), &matchers)
		if len(matchers) != 1 {
			t.Fatalf("preset registered twice %s", rawMain(declarations))
		}
		var hooks []protocol.JSONObject
		_ = json.Unmarshal(matchers[0].Get("hooks"), &hooks)
		if len(hooks) != 1 {
			t.Fatalf("preset hook duplicate %s", rawMain(declarations))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
