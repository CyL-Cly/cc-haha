package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualTeamToolsRetainCatalogueRuntimeAndPhysicalTaskErrorPrecedence(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CC_HAHA_TEAM_REVIEW_REQUIRED"] = "1"
		n.Environment["CC_HAHA_TEAM_LEADER_RUNTIME"] = `{"providerId":"fixture-provider","modelId":"fixture-model"}`
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read,TeamCreate,TeamPlan,TeamDelete"} })
	team := "task-capture-order"
	_ = callActualTeamTool(t, m, "TeamCreate", `{"team_name":"task-capture-order","plan":{"members":[{"id":"m","name":"worker","prompt":"actual"}],"tasks":[]}}`)
	_, err := m.taskStore.Create(team, objMain([]byte(`{"subject":"outside dependency","description":"actual","status":"pending","owner":"worker","blocks":[],"blockedBy":["missing"]}`)))
	if err != nil {
		t.Fatal(err)
	}
	var failures []string
	if err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		n := m.options.Native
		m.agents, err = agentcatalog.New(agentcatalog.Options{Owner: m.Owner, Files: nativeAgentFiles{n}, Home: n.Home, CWD: n.CWD, ProjectRoot: n.CWD, ConfigHome: n.Environment["CLAUDE_CONFIG_DIR"], ManagedRoot: n.ManagedRoot, Environment: func(key string) string { return n.Environment[key] }, Now: n.Now, Settings: func(string) (*protocol.JSONObject, error) { return nil, errors.New("ACTUAL_CATALOG_READ_UNAVAILABLE") }})
		if err != nil {
			return err
		}
		n.Environment["CC_HAHA_TEAM_LEADER_RUNTIME"] = "invalid json"
		for _, proposal := range []*protocol.JSONObject{nil, func() *protocol.JSONObject { v := objMain([]byte(`{"members":[],"tasks":[]}`)); return &v }()} {
			_, _, failure := m.nativeTeamProposal(scope, proposal, "fixture-model")
			if failure == nil {
				return errors.New("invalid configuration unexpectedly accepted")
			}
			failures = append(failures, failure.Error())
		}
		future, err := m.startNativeTeamPlan(context.Background(), scope, objMain([]byte(`{"team_name":"task-capture-order","operation":"submit","expected_revision":1}`)))
		if err != nil {
			return err
		}
		_, failure := future.Await(scope)
		if failure == nil {
			return errors.New("outside proposal dependency unexpectedly accepted")
		}
		failures = append(failures, failure.Error())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-tool-order.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_TOOL_ORDER_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=fake-private-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(data))
	}
	var source struct {
		Errors       []string
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(data), stderr.String())
	}
	if !reflect.DeepEqual(failures, source.Errors) {
		t.Fatalf("Go%q Source%q", failures, source.Errors)
	}
}
