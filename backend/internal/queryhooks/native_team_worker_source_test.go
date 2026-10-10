package queryhooks

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualMainFrontmatterKeepsStopSessionGroupingOrderAndRepeatedRegistrationMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	parse := func(text string) protocol.JSONObject {
		t.Helper()
		var o protocol.JSONObject
		if err := o.UnmarshalJSON([]byte(text)); err != nil {
			t.Fatal(err)
		}
		return o
	}
	main := parse(`{"SubagentStop":[{"matcher":"worker","hooks":[{"type":"command","command":"subagent"}]}],"Stop":[{"hooks":[{"type":"command","command":"main"},{"type":"command","command":"second"}]},{"matcher":"other","hooks":[{"type":"command","command":"other"}]},{"hooks":[{"type":"command","command":"same matcher"}]}],"FutureEvent":[{"hooks":[{"type":"command","command":"ignored unknown event"}]}]}`)
	all := protocol.NewJSONObject()
	for _, event := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "Notification", "UserPromptSubmit", "SessionStart", "SessionEnd", "Stop", "StopFailure", "SubagentStart", "SubagentStop", "PreCompact", "PostCompact", "PermissionRequest", "PermissionDenied", "Setup", "TeammateIdle", "TaskCreated", "TaskCompleted", "Elicitation", "ElicitationResult", "ConfigChange", "WorktreeCreate", "WorktreeRemove", "InstructionsLoaded", "CwdChanged", "FileChanged"} {
		all.Set(event, commandJSON([]protocol.JSONObject{parse(`{"hooks":[{"type":"command","command":"actual declaration"}]} `)}))
	}
	registrations := [][]protocol.JSONObject{{main}, {main, main}, {all}, {parse(`{"Stop":[],"PostToolUse":null}`)}, {parse(`{"Stop":[{"matcher":"","hooks":[]} ]}`)}, {parse(`{"Stop":[{"matcher":"","hooks":[{"type":"http","url":"http://127.0.0.1:1"}]}]}`)}}
	rows := []protocol.JSONObject{}
	for _, values := range registrations {
		rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "sid", Value: commandJSON("actual-main-session")}, protocol.JSONField{Name: "registrations", Value: commandJSON(values)}))
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-main-frontmatter-hooks.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MAIN_FRONTMATTER_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(commandJSON(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Observations []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Observations) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	for i, values := range registrations {
		service := &Service{options: Options{Commands: &CommandBindings{Store: &CommandStore{events: map[string]map[string][]commandMatcher{}}}}}
		for _, hooks := range values {
			if err = service.RegisterNativeMainHooks("actual-main-session", hooks); err != nil {
				t.Fatal(err)
			}
		}
		actual := commandJSON(service.NativeMainHookDeclarations("actual-main-session"))
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Observations[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case %d Go %s Source %s", i, actual, source.Observations[i])
		}
	}
}
