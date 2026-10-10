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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstate"
)

func TestActualNativeSessionStateEventsHonorLiveEnvInteractiveBoundedQueueAndOriginalMetadata(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	type step struct {
		Flag    string               `json:"flag"`
		State   string               `json:"state"`
		Details *protocol.JSONObject `json:"details,omitempty"`
	}
	type row struct {
		Interactive bool   `json:"interactive"`
		Steps       []step `json:"steps"`
	}
	rows := []row{}
	for _, interactive := range []bool{false, true} {
		for _, flag := range []string{"", "0", "false", "1", "TRUE", " yes ", "on", "off"} {
			rows = append(rows, row{interactive, []step{{flag, "running", nil}, {flag, "requires_action", ptrMainObj(`{"tool_name":"Write","action_description":"real file","tool_use_id":"tool","request_id":"request"}`)}, {flag, "running", nil}, {"false", "running", nil}, {flag, "idle", nil}}})
		}
	}
	large := row{}
	for i := 0; i < 1002; i++ {
		value := "running"
		if i%2 == 1 {
			value = "idle"
		}
		large.Steps = append(large.Steps, step{"1", value, nil})
	}
	rows = append(rows, large)
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", nil, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read"} })
	observations := []protocol.JSONObject{}
	for _, row := range rows {
		var effects, events []protocol.JSONObject
		effects = []protocol.JSONObject{}
		events = []protocol.JSONObject{}
		err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			m.options.Interactive = row.Interactive
			m.nativeSDKStateEvents = nil
			options := m.nativeSessionStateOptions()
			options.OnState = func(_ *cooperative.Scope, state string, details *protocol.JSONObject) error {
				v := protocol.NewJSONObject(protocol.JSONField{Name: "state", Value: quoteMain(state)})
				if details != nil {
					v.Set("details", rawMain(*details))
				}
				effects = append(effects, v)
				return nil
			}
			options.OnMetadata = func(_ *cooperative.Scope, value protocol.JSONObject) error {
				effects = append(effects, protocol.NewJSONObject(protocol.JSONField{Name: "metadata", Value: rawMain(value)}))
				return nil
			}
			worker, err := sessionstate.New(m.Owner, options)
			if err != nil {
				return err
			}
			for _, step := range row.Steps {
				if err = m.environment.SetProperty(quoteMain("CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS"), quoteMain(step.Flag)); err != nil {
					return err
				}
				if err = worker.Change(scope, step.State, step.Details); err != nil {
					return err
				}
			}
			start := len(m.outputs)
			if err = m.nativeDrainSessionEvents(scope); err != nil {
				return err
			}
			for _, bytes := range m.outputs[start:] {
				event := objMain(bytes)
				if event.String("uuid") == "" || event.String("session_id") != m.Controller.SessionID() {
					t.Error("missing genuine drain envelope")
				}
				event.Delete("uuid")
				event.Delete("session_id")
				events = append(events, event)
			}
			current, err := worker.Current(scope)
			observations = append(observations, protocol.NewJSONObject(protocol.JSONField{Name: "effects", Value: rawMain(effects)}, protocol.JSONField{Name: "events", Value: rawMain(events)}, protocol.JSONField{Name: "current", Value: quoteMain(current)}))
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-state-events.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SESSION_EVENTS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows         []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	for i, got := range observations {
		var a, b any
		_ = json.Unmarshal(rawMain(got), &a)
		_ = json.Unmarshal(rawMain(source.Rows[i]), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("state/queue/metadata Source differs row %d: Go %s Source %s", i, rawMain(got), rawMain(source.Rows[i]))
		}
	}
}

func ptrMainObj(raw string) *protocol.JSONObject { value := objMain([]byte(raw)); return &value }
