package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSDKMCPInitializeRegistrationActualOriginalOuterProducer(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(home, ".claude"), 0700); e != nil {
		t.Fatal(e)
	}
	fixture, _ := filepath.Abs("../../testdata/native-sdk-mcp-registration.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, finish := context.WithTimeout(context.Background(), 10*time.Second)
	defer finish()
	rows := []json.RawMessage{raw([]string{"peer", "2", "peer", "__proto__", "constructor"}), raw([]any{7, nil, true, []any{1, nil, "x"}, map[string]any{}}), json.RawMessage(`"a\ud83d\ude00\ud800"`), raw([]any{}), raw(false), raw(nil), raw(map[string]any{}), raw(map[string]any{"length": 1}), raw([]any{map[string]any{"toString": 7}})}
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_MCP_REGISTRATION_FIXTURE=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(raw(map[string]any{"rows": rows}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, e := command.Output()
	if e != nil {
		t.Fatal("actual Source registration", e, stderr.String())
	}
	var source []struct {
		Configs protocol.JSONObject
		Error   string
	}
	if e = json.Unmarshal(data, &source); e != nil || len(source) != len(rows) {
		t.Fatal(e, string(data))
	}
	t.Logf("actual Source registration rows %s", data)
	for i, names := range rows {
		owner := cooperative.New()
		state, e := NewNativeSDKMCPState(owner)
		if e != nil {
			t.Fatal(e)
		}
		h := &Host{options: Options{Owner: owner, NativeSDKMCP: state, OnPhase: func(string) {}, Output: Output{Enqueue: func([]byte) error { return nil }}, NativeSDKHookInitialize: func(*cooperative.Scope) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }}, sdkHooks: &nativeSDKHookState{initialized: true}}
		var got protocol.JSONObject
		e = owner.Run(ctx, func(scope *cooperative.Scope) error {
			request := protocol.NewJSONObject(protocol.JSONField{Name: "sdkMcpServers", Value: names})
			// Re-initialize still runs the outer name producer before the original
			// Already initialized response. No metadata or model is constructed.
			e := h.sdkHookInitializeControl(scope, protocol.NewJSONObject(protocol.JSONField{Name: "request", Value: raw(request)}))
			got, _ = state.Configurations(scope)
			return e
		})
		errText := ""
		if e != nil {
			errText = e.Error()
		}
		if errText != source[i].Error {
			owner.Close()
			t.Fatalf("row %d Source rejection got %q want %q", i, errText, source[i].Error)
		}
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(raw(got)), protocol.StringifyValidatedJavaScriptJSON(raw(source[i].Configs))) {
			owner.Close()
			t.Fatalf("row %d Source configs got %s want %s", i, raw(got), raw(source[i].Configs))
		}
		owner.Close()
	}
}
