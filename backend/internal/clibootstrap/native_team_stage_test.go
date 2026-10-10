package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestActualAgentTeamDraftStagingAliasPrecedenceAndOriginalMapErrorMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	inputs := []map[string]any{
		{"input": map[string]any{"description": "record member", "prompt": "first task", "name": "worker"}},
		{"input": map[string]any{"description": "replace same", "prompt": "revised task", "name": "worker", "model": "sonnet"}},
		{"envModel": "haiku", "input": map[string]any{"description": "env wins", "prompt": "other task", "name": "second", "team_name": "draft-stage", "model": "opus", "mode": "plan", "isolation": "worktree"}},
		{"envModel": "inherit", "input": map[string]any{"description": "skip inherit", "prompt": "third task", "name": "third"}},
		{"input": map[string]any{"description": "invalid name", "prompt": "invalid", "name": "bad name"}},
		{"input": map[string]any{"description": "missing type", "prompt": "unavailable", "name": "missing", "subagent_type": "absent"}},
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-agent-stage.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_STAGE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=fake-private-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "ANTHROPIC_DEFAULT_SONNET_MODEL=fixture-sonnet", "ANTHROPIC_DEFAULT_HAIKU_MODEL=fixture-haiku", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "rows": inputs}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(inputs) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CC_HAHA_TEAM_REVIEW_REQUIRED"] = "1"
		n.Environment["CC_HAHA_TEAM_LEADER_RUNTIME"] = `{"providerId":"fixture-provider","modelId":"fixture-model","effortLevel":"high"}`
		n.Environment["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "fixture-sonnet"
		n.Environment["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "fixture-haiku"
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"TeamCreate,TeamPlan,TeamDelete,Agent"} })
	callActualTeamTool(t, m, "TeamCreate", `{"team_name":"draft-stage"}`)
	for i, row := range inputs {
		var data protocol.JSONObject
		var failure, mapFailure error
		if err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			if model, present := row["envModel"].(string); present {
				m.options.Native.Environment["CLAUDE_CODE_SUBAGENT_MODEL"] = model
			} else {
				delete(m.options.Native.Environment, "CLAUDE_CODE_SUBAGENT_MODEL")
			}
			_, tools, err := m.nativeTools.Catalog(scope)
			if err != nil {
				return err
			}
			for _, tool := range tools {
				if tool.Name == "Agent" {
					var input protocol.JSONObject
					_ = input.UnmarshalJSON(rawMain(row["input"]))
					future, err := tool.StartCallJSON(context.Background(), input, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
					if err != nil {
						failure = err
						return nil
					}
					result, err := future.Await(scope)
					failure = err
					if err == nil {
						_ = data.UnmarshalJSON(result.Data)
						_, mapFailure = builtintools.MapResult("Agent", result.Data, quoteMain("stage-id"))
					}
					break
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		data.Delete("plan_id")
		plan, err := m.teamLeader.plans.Read("draft-stage")
		if err != nil {
			t.Fatal(err)
		}
		var dataValue, errorValue, mapError any
		if data.IsObject() {
			dataValue = data
		}
		if failure != nil {
			errorValue = failure.Error()
		}
		if mapFailure != nil {
			mapError = mapFailure.Error()
		}
		actual := rawMain(map[string]any{"data": dataValue, "error": errorValue, "mapError": mapError, "state": plan.String("state"), "revision": plan.Get("revision"), "members": plan.Get("members")})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case %d Go %s Source %s", i, strings.ReplaceAll(string(actual), m.options.Native.Home, "$HOME"), source.Rows[i])
		}
	}
}
