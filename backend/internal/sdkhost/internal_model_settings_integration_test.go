package sdkhost_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualNativeMenuWritesTwoASKsRetainOriginalPermissionsAndRejectUnknownSettings(t *testing.T) {
	var mu sync.Mutex
	bodies := []json.RawMessage{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"actual-settings-response\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"actual settings request\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, work, managed := filepath.Join(root, "home"), filepath.Join(root, "work"), filepath.Join(root, "managed")
	config := filepath.Join(home, ".claude")
	for _, path := range []string{config, work, managed} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"model":"original-raw","effortLevel":"medium","future":{"kept":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config, "ANTHROPIC_BASE_URL": server.URL, "ANTHROPIC_API_KEY": "fake-only-loopback", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "PATH": "/usr/bin:/bin", "TZ": "UTC", "ANTHROPIC_DEFAULT_SONNET_MODEL": "actual-first-model", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES": "effort,max_effort", "ANTHROPIC_DEFAULT_OPUS_MODEL": "actual-second-model", "ANTHROPIC_DEFAULT_OPUS_MODEL_SUPPORTED_CAPABILITIES": "effort,max_effort"}
	native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: work, Platform: "darwin", ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	process, err := clibootstrap.NewMainPrint(clibootstrap.MainPrintOptions{Native: native, PID: os.Getpid(), Version: "999.0.0-sdk-native-cache", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: clibootstrap.PrintArguments{Model: "sonnet", SystemPrompt: "Exercise the actual shared Go Harness.", SessionID: "62ab0a91-90d1-446e-974c-7a5be62464a7", Tools: []string{"Read,Write,Edit,Bash,Glob,Grep"}, MaxTurns: 20, PermissionMode: "bypassPermissions", Sources: clibootstrap.DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if err := process.StartSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	var originalSettings *protocol.JSONObject
	if err := process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var err error
		originalSettings, _, err = process.Controller.SDKSettingsOnOwner(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	turn := func(text string) protocol.JSONObject {
		message, _ := json.Marshal(map[string]any{"role": "user", "content": text})
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: []byte(`"user"`)}, protocol.JSONField{Name: "message", Value: message})
	}
	if err := process.RunTurn(context.Background(), turn("first ASK")); err != nil {
		t.Fatal(err)
	}
	if err := process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		return process.Controller.SetSDKPermissionMode(scope, "acceptEdits")
	}); err != nil {
		t.Fatal(err)
	}
	picker, err := process.NativeModelPicker(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	model, effort := "opus", "low"
	if _, err := process.ApplyNativeModelPicker(context.Background(), picker.ID, tui.ModelPickerResult{Model: &model, Effort: &effort, HasToggledEffort: true}); err != nil {
		t.Fatal(err)
	}
	if err := process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		current, permission, err := process.Controller.SDKSettingsOnOwner(scope)
		if err == nil && (current != originalSettings || current.String("model") != "original-raw" || permission.String("mode") != "acceptEdits") {
			return fmt.Errorf("internal write replaced Source AppState settings/permission: %s %s", current.Get("model"), permission.Get("mode"))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := process.RunTurn(context.Background(), turn("second ASK")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	captured := append([]json.RawMessage(nil), bodies...)
	mu.Unlock()
	if len(captured) != 2 || object(captured[0]).String("model") != "actual-first-model" || object(captured[1]).String("model") != "actual-second-model" || object(object(captured[1]).Get("output_config")).String("effort") != "low" {
		t.Fatalf("two actual shared requests did not consume menu settings: %#v", captured)
	}
	if err := process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		merged, err := process.Startup.GetInitialSettings()
		if err != nil {
			return err
		}
		merged.Set("language", []byte(`"changed-unconsumed-field"`))
		if err := process.Host.AcceptInternalModelSettings(scope); err == nil {
			return fmt.Errorf("unrelated consumer was admitted as a model write")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := process.RunTurn(context.Background(), turn("must stop before physical request")); err == nil || !strings.Contains(err.Error(), "cache identity") {
		t.Fatalf("unknown settings lifecycle bypassed: %v", err)
	}
	mu.Lock()
	count := len(bodies)
	mu.Unlock()
	if count != 2 {
		t.Fatalf("menu or unknown cache opened another physical request: %d", count)
	}
}
