package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const fullPaneSourceBinary = `#!/usr/bin/python3
import os,sys,json
args=sys.argv[1:]
with open(os.environ['CC_HAHA_PANE_LOG'],'a') as f: f.write(json.dumps(args)+'\n')
if args[:1]==['-L']:args=args[2:]
cmd=args[0]
if cmd=='has-session':sys.exit(1)
if cmd=='new-session':print('%0')
elif cmd=='list-panes':print('%0')
`

func TestActualNativePaneFullSpawnProducerMatchesOriginalSourceRosterTaskMailboxAndCommand(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Source Bun required")
	}
	home := t.TempDir()
	directory := filepath.Join(home, "bin")
	log := filepath.Join(home, "args")
	_ = os.Mkdir(directory, 0700)
	_ = os.WriteFile(filepath.Join(directory, "tmux"), []byte(fullPaneSourceBinary), 0755)
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		n.Environment["PATH"] = directory + ":/usr/bin:/bin"
		n.Environment["CC_HAHA_PANE_LOG"] = log
		n.Environment["CLAUDE_CODE_TEAMMATE_COMMAND"] = "/private/actual-cli"
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		delete(n.Environment, "TMUX")
		delete(n.Environment, "ITERM_SESSION_ID")
		delete(n.Environment, "TERM_PROGRAM")
	}, func(o *MainPrintOptions) {
		o.Interactive = true
		o.Arguments.TeammateMode = "tmux"
		o.Arguments.Tools = []string{"TeamCreate,TeamDelete,Agent,SendMessage"}
		o.Arguments.EffortLevel = "high"
		o.Error = io.Discard
	})
	callActualTeamTool(t, m, "TeamCreate", `{"team_name":"actual-source-pane"}`)
	input := objMain(rawMain(map[string]string{"name": "worker", "prompt": "actual full Source pane producer initial message", "subagent_type": "general-purpose"}))
	var initialTeam, initialContext, result, task, roster, file protocol.JSONObject
	var mailbox []protocol.JSONObject
	now := m.options.Native.NowMillis()
	err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		initialTeam = m.teamLeader.config.Read("actual-source-pane").Clone()
		initialContext = m.teamState.snapshot.Clone()
		f, err := m.stageNativeTeamAgent(context.Background(), scope, "actual-source-pane", input, queryloop.JSONToolCallContext{ToolUseID: quoteMain("physical-pane-call")})
		if err != nil {
			return err
		}
		v, err := f.Await(scope)
		if err != nil {
			return err
		}
		_ = result.UnmarshalJSON(v.Data)
		roster = m.teamState.snapshot.Clone()
		tasks, err := m.Controller.BackgroundTasksOnOwner(scope)
		if err != nil {
			return err
		}
		task = tasks[0].Clone()
		file = m.teamLeader.config.Read("actual-source-pane").Clone()
		_ = json.Unmarshal(m.teamLeader.mailbox.Read("worker", "actual-source-pane"), &mailbox)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceHome, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-pane-spawn.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = sourceHome
	command.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_TEAM_PANE_FIXTURE=1", "CC_HAHA_AGENT_TEAMS_ENABLED=1", "CLAUDE_CODE_ENABLE_TASKS=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin", "ANTHROPIC_BASE_URL=http://127.0.0.1:1"}
	sourceInput := input.Clone()
	sourceInput.Set("agent_type", sourceInput.Get("subagent_type"))
	sourceInput.Delete("subagent_type")
	sourceInput.Set("team_name", quoteMain("actual-source-pane"))
	sourceInput.Set("plan_mode_required", rawMain(false))
	command.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": sourceHome, "backend": "tmux", "inside": false, "binaryScript": fullPaneSourceBinary, "producer": map[string]any{"sessionId": m.Controller.SessionID(), "binary": "/private/actual-cli", "now": now, "team": initialTeam, "teamContext": initialContext, "input": sourceInput, "toolUseId": "physical-pane-call", "leaderModel": m.options.Arguments.Model, "effort": "high", "mode": "bypassPermissions", "thinkingConfig": map[string]string{"type": "disabled"}}}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(out))
	}
	var source struct {
		Producer     map[string]json.RawMessage
		Args         [][]string
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out), stderr.String())
	}
	normalize := func(raw json.RawMessage) any {
		var v any
		_ = json.Unmarshal(raw, &v)
		var walk func(any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for _, key := range []string{"id", "outputFile", "timestamp", "spawnedAt", "joinedAt", "startTime"} {
					if _, ok := x[key]; ok {
						x[key] = "original-variable"
					}
				}
				for key, value := range x {
					if key == "cwd" {
						x[key] = "CWD"
					} else {
						walk(value)
					}
				}
			case []any:
				for _, value := range x {
					walk(value)
				}
			}
		}
		walk(v)
		return v
	}
	result.Delete("status")
	result.Delete("prompt")
	actual := map[string]any{"result": map[string]any{"data": result}, "task": task, "context": roster, "team": file, "mailbox": mailbox}
	for key, value := range actual {
		if !reflect.DeepEqual(normalize(source.Producer[key]), normalize(rawMain(value))) {
			t.Fatalf("%s original Source full producer differs\nSource=%s\nGo=%s", key, source.Producer[key], rawMain(value))
		}
	}
	data, _ := os.ReadFile(log)
	goArgs := [][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var args []string
		_ = json.Unmarshal([]byte(line), &args)
		for i, value := range args {
			if strings.HasPrefix(value, "claude-swarm-") {
				args[i] = "claude-swarm-PID"
			}
			if strings.HasPrefix(value, "cd ") {
				args[i] = strings.ReplaceAll(strings.ReplaceAll(value, m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "CONFIG"), m.workingDirectory(), "CWD")
			}
		}
		goArgs = append(goArgs, args)
	}
	for _, args := range source.Args {
		for i, value := range args {
			if strings.HasPrefix(value, "cd ") {
				args[i] = strings.ReplaceAll(strings.ReplaceAll(value, filepath.Join(sourceHome, ".claude"), "CONFIG"), sourceHome, "CWD")
			}
		}
	}
	if !reflect.DeepEqual(source.Args, goArgs) {
		t.Fatalf("Source full producer physical argv differs\nSource=%v\nGo=%v", source.Args, goArgs)
	}
}
