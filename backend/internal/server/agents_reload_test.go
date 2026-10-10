package server

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type reloadedSession struct {
	transportSession
	response protocol.JSONObject
	failure  error
	calls    int
}

func (session *reloadedSession) ControlComponents(_ context.Context, subtype, name string) (protocol.JSONObject, error) {
	if subtype != "reload_plugins" || name != "" {
		return protocol.JSONObject{}, errors.New("incorrect component reload request")
	}
	session.calls++
	return session.response.Clone(), session.failure
}

func TestAgentReloadUsesExistingRuntimeAndReportsAppliedCountsOrFailure(t *testing.T) {
	opts := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		return nil, errors.New("must not create a replacement session")
	})
	service, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	var response protocol.JSONObject
	if err := response.UnmarshalJSON([]byte(`{"commands":["skill", " ",null,{"command":"other"},{"name":""}],"agents":[{},{}],"plugins":[{}],"mcpServers":[{},{},{}],"error_count":4}`)); err != nil {
		t.Fatal(err)
	}
	live := &reloadedSession{response: response}
	service.runtimes["actual"] = &runtimeSession{live: live, cancel: func() {}}
	result, err := service.ReloadSessionComponents(context.Background(), "missing")
	if err != nil || result.String("reason") != "not_running" || live.calls != 0 {
		t.Fatal(result, err, live.calls)
	}
	result, err = service.ReloadSessionComponents(context.Background(), "actual")
	var fields map[string]any
	raw, _ := result.MarshalJSON()
	_ = json.Unmarshal(raw, &fields)
	want := map[string]any{"applied": true, "commands": float64(2), "agents": float64(2), "plugins": float64(1), "mcpServers": float64(3), "errors": float64(4)}
	if err != nil || !reflect.DeepEqual(fields, want) || live.calls != 1 {
		t.Fatal(fields, err, live.calls)
	}
	live.failure = errors.New("actual owner reload failure")
	result, err = service.ReloadSessionComponents(context.Background(), "actual")
	if err != nil || result.String("reason") != "failed" || result.String("error") != live.failure.Error() || live.calls != 2 {
		t.Fatal(result, err, live.calls)
	}
	service.runtimes["actual"].retired = true
	result, err = service.ReloadSessionComponents(context.Background(), "actual")
	if err != nil || result.String("reason") != "not_running" || live.calls != 2 {
		t.Fatal(result, err, live.calls)
	}
}

func TestSlashCommandsNormalizationMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("requires explicit pinned Bun to compare original Source function")
	}
	input := json.RawMessage(`[null,7,false,[],{},""," ","\ufeff", "\u0085", " raw name ","\ud800",{"name":"named","description":"\ud800","argumentHint":" x ","ignored":true},{"command":"fallback","description":7},{"name":null,"command":"valid"},{"name":7,"command":"valid"},{"name":"","command":"must-not-fallback"},{"name":"valid","description":null,"argumentHint":null}]`)
	private := t.TempDir()
	fixture, err := filepath.Abs("testdata/slash_commands_source.ts")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Env = []string{"HOME=" + private, "USERPROFILE=" + private, "CLAUDE_CONFIG_DIR=" + filepath.Join(private, ".claude"), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Source normalization %s: %v", output, err)
	}
	want := []protocol.JSONObject{}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(string(output), err)
	}
	actual := NormalizeSessionSlashCommands(input)
	for _, rows := range [][]protocol.JSONObject{actual, want} {
		for _, item := range rows {
			for _, name := range []string{"name", "description", "argumentHint"} {
				value := item.Get(name)
				if value == nil {
					continue
				}
				units, err := protocol.DecodeJavaScriptJSONString(value)
				if err != nil {
					t.Fatal(err)
				}
				item.Set(name, protocol.QuoteJavaScriptUTF16(units))
			}
		}
	}
	left, _ := json.Marshal(actual)
	right, _ := json.Marshal(want)
	if string(left) != string(right) {
		t.Fatalf("Go=%s Source=%s", left, right)
	}
}
