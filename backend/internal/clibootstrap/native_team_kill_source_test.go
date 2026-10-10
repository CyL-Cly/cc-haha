package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func TestNativeKillInProcessStateRosterWaitersAndClosingBookendAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	rows := []protocol.JSONObject{}
	for _, task := range []string{
		`{"id":"actual","type":"in_process_teammate","status":"running","description":"actual live task","toolUseId":"parent-tool","messages":[{"type":"user","uuid":"first"},{"type":"assistant","uuid":"last"}],"pendingUserMessages":["pending"],"inProgressToolUseIDs":["actual-tool"],"isIdle":false,"extra":{"preserved":true}}`,
		`{"id":"actual","type":"in_process_teammate","status":"running","description":"empty task","messages":[],"pendingUserMessages":[],"isIdle":true}`,
		`{"id":"actual","type":"in_process_teammate","status":"completed","notified":true,"messages":[],"pendingUserMessages":[],"isIdle":true}`,
		`{"id":"actual","type":"local_agent","status":"running","description":"ordinary Agent","messages":[],"pendingUserMessages":[]}`,
	} {
		rows = append(rows, objMain(rawMain(map[string]any{"task": objMain([]byte(task)), "now": 1700000000123})))
	}
	extra := rows[0].Clone()
	extra.Set("duplicate", rawMain(true))
	rows = append(rows, extra)
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CC_HAHA_TEAM_REVIEW_REQUIRED"] = "1"
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read,SendMessage"} })
	actual := []protocol.JSONObject{}
	for i, row := range rows {
		team := fmt.Sprintf("kill-%d", i)
		identity := objMain(rawMain(map[string]any{"agentId": "worker@" + team, "teamName": team, "agentName": "worker"}))
		members := []map[string]string{{"agentId": "worker@" + team, "name": "worker"}, {"agentId": "other", "name": "other"}}
		if string(row.Get("duplicate")) == "true" {
			members = []map[string]string{{"agentId": "worker@" + team, "name": "worker"}, {"agentId": "worker@" + team, "name": "duplicate"}, {"agentId": "other", "name": "other"}}
		}
		if err := m.teamLeader.config.Write(team, objMain(rawMain(map[string]any{"name": team, "createdAt": 1700000000123, "members": members}))); err != nil {
			t.Fatal(err)
		}
		life, cancel := context.WithCancelCause(context.Background())
		cleanup, idle := 0, 0
		m.outputMu.Lock()
		frameStart := len(m.outputs)
		m.outputMu.Unlock()
		var observation protocol.JSONObject
		if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			m.teamState = &nativeTeamState{snapshot: objMain(rawMain(map[string]any{"teamName": team, "teammates": map[string]any{"worker@" + team: map[string]string{"name": "worker"}, "other": map[string]string{"name": "other"}}}))}
			task, _ := row.Object("task")
			task.Set("identity", rawMain(identity))
			entry := &sessioncontroller.BackgroundTask{Data: task, Cancel: cancel, IdleCallbacks: []func(){func() { idle++ }}, ReadOutput: func(context.Context) (json.RawMessage, error) { return nil, nil }, Join: func(context.Context) error { return nil }}
			entry.Stop = func(next *cooperative.Scope, now float64) error {
				return m.killNativeTeammateOnOwner(next, "actual", now, func(*cooperative.Scope) (bool, error) { cleanup++; return true, nil })
			}
			if err := m.Controller.RegisterBackgroundTaskOnOwner(scope, entry); err != nil {
				return err
			}
			killed := task.String("type") == "in_process_teammate" && task.String("status") == "running"
			if killed {
				if err := entry.Stop(scope, 1700000000123); err != nil {
					return err
				}
			}
			stored, err := m.Controller.BackgroundTaskOnOwner(scope, "actual")
			if err != nil {
				return err
			}
			data := stored.Data.Clone()
			data.Delete("onIdleCallbacks")
			observation = objMain(rawMain(map[string]any{"killed": killed, "task": data, "context": m.teamState.snapshot, "aborted": life.Err() != nil, "cleanup": cleanup, "idle": idle}))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		config := m.teamLeader.config.Read(team)
		observation.Set("members", config.Get("members"))
		m.outputMu.Lock()
		frames := append([][]byte{}, m.outputs[frameStart:]...)
		m.outputMu.Unlock()
		sdk := []protocol.JSONObject{}
		for _, raw := range frames {
			frame := objMain(raw)
			frame.Delete("uuid")
			frame.Delete("session_id")
			sdk = append(sdk, frame)
		}
		observation.Set("sdk", rawMain(sdk))
		actual = append(actual, observation)
		cancel(nil)
		if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { return m.Controller.DeleteBackgroundTaskOnOwner(scope, "actual") }); err != nil {
			t.Fatal(err)
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-inprocess-teammate-kill.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_KILL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Capabilities.Blocked) > 0 || len(source.Rows) != len(actual) {
		t.Fatal(err, string(output), stderr.String())
	}
	for i, row := range actual {
		var a, b any
		_ = json.Unmarshal(rawMain(row), &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case%d Go%s\nSource%s", i, rawMain(row), source.Rows[i])
		}
	}
}
