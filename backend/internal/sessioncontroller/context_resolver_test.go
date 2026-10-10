package sessioncontroller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

func TestResolveContextIsUnpreparedAndAllocatesOnlyOriginalMeta(t *testing.T) {
	controller := activeController(t)
	historyBefore := marshal(t, controller.History())
	input := requestcontext.Context{Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: "fixture-model"}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: map[string]string{"ANTHROPIC_API_KEY": "fixture", "ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}, DeviceID: "fixture-device"}}
	resolved, err := controller.ResolveContext(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.History) != 1 || resolved.Options.QuerySource != "sdk" || !resolved.Options.IsNonInteractiveSession || resolved.Host.SessionID != controller.SessionID() {
		t.Fatal("original ASK bindings lost")
	}
	original := marshal(t, resolved.History)
	prepared, err := requestcontext.Prepare(resolved, requestcontext.Dependencies{UUID: controller.options.UUID, Now: controller.options.Now})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err = prepared.BuildAttempt(requestcontext.AttemptOptions{Model: "fixture-model"}); err != nil {
			t.Fatal(err)
		}
	}
	if original != marshal(t, resolved.History) || historyBefore != marshal(t, controller.History()) {
		t.Fatal("retry/context prefix polluted mutable transcript")
	}
	second, err := controller.ResolveContext(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if string(second.History[0].Get("uuid")) == string(resolved.History[0].Get("uuid")) {
		t.Fatal("new ASK reused meta allocation")
	}
	resolved.History[0].Set("future", []byte("true"))
	if controller.History() != nil && len(controller.History()) > 0 {
		t.Fatal("prefix leaked into original history")
	}
}
func TestResolveContextRejectsUnknownHistoryAndActualMemoryProducer(t *testing.T) {
	controller := activeController(t)
	if _, err := controller.ResolveContext(context.Background(), requestcontext.Context{History: []protocol.JSONObject{}}); err == nil {
		t.Fatal("alternate history accepted")
	}
	if _, err := controller.ResolveContext(context.Background(), requestcontext.Context{Tools: []protocol.JSONObject{protocol.NewJSONObject()}}); err == nil {
		t.Fatal("host tools escaped capability")
	}
	service := controller.options.UserContext
	controller.options.UserContext = nil
	if _, err := controller.ResolveContext(context.Background(), requestcontext.Context{}); err == nil {
		t.Fatal("missing original memo silently replaced")
	}
	controller.options.UserContext = service
	home := t.TempDir()
	options := hostOptions(t, home).options
	options.ResumeOptions.Environment["CLAUDE_CODE_SIMPLE"] = "0"
	controller, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = controller.Activate(activation()); err != nil {
		t.Fatal(err)
	}
	defer controller.AwaitBackground()
	_, err = controller.ResolveContext(context.Background(), requestcontext.Context{})
	var capability *usercontext.CapabilityError
	if !errors.As(err, &capability) {
		t.Fatal("active discovery returned fake no-memory", err)
	}
}
func TestControllerContextUsesRealSettingsModeAndUnknownSchemaGuard(t *testing.T) {
	home := t.TempDir()
	options := hostOptions(t, home).options
	controller, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = controller.Activate(activation()); err != nil {
		t.Fatal(err)
	}
	defer controller.AwaitBackground()
	path := filepath.Join(home, ".claude", "settings.json")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(`{"pluginConfigs":{"agents-md@builtin":{"options":{"instructionFiles":"managed-only"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	resolved, err := controller.ResolveContext(context.Background(), requestcontext.Context{})
	if err != nil || len(resolved.History) != 1 {
		t.Fatal(err)
	}
	mode, _ := controller.options.UserContext.CachedMode()
	if mode != "managed-only" {
		t.Fatal("mode supplied instead of independently derived")
	}
	if err = os.WriteFile(path, []byte(`{"statusLine":{"type":"command","command":"must-not-run"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	options.ResumeOptions.SettingsState.ResetSettingsCache()
	resolved, err = controller.ResolveContext(context.Background(), requestcontext.Context{})
	if err != nil || len(resolved.History) != 1 {
		t.Fatalf("statusLine setting blocked context resolution: %v", err)
	}
}
func TestLegacyControllerContextDoesNotAcceptMissingBindings(t *testing.T) {
	options := hostOptions(t, t.TempDir()).options
	options.ResumeOptions.SettingsState = nil
	if _, err := New(options); err == nil {
		t.Fatal("missing cache admitted")
	}
	options = hostOptions(t, t.TempDir()).options
	options.ResumeOptions.ConfigDir = "relative"
	if _, err := New(options); err == nil {
		t.Fatal("unresolved original path admitted")
	}
}

func TestLegacyControllerPolicyReadsOnlyActualEmptyObjects(t *testing.T) {
	for _, data := range []string{`{}`, `null`, `[]`, `{"disableAllHooks":true}`, `{`, `{"future":"\\ud800"}`} {
		t.Run(data, func(t *testing.T) {
			home := t.TempDir()
			options := hostOptions(t, home).options
			path := options.ResumeOptions.PolicyPaths[0]
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := New(options)
			if (err == nil) != (data == `{}`) {
				t.Fatal("policy boundary changed", data, err)
			}
		})
	}
}
