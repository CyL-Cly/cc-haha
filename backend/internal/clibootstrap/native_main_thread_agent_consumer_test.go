package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualMainPrintAgentSettingsOwnerHotReloadAndModelValidation(t *testing.T) {
	var mu sync.Mutex
	bodies := []json.RawMessage{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, append(json.RawMessage(nil), data...))
		mu.Unlock()
		if bytes.Contains(data, []byte(`"Hi"`)) {
			w.Header().Set("content-type", "application/json")
			_, _ = io.WriteString(w, `{"id":"actual-agent-model-validation","type":"message","role":"assistant","content":[{"type":"text","text":"Hi"}],"model":"fixture-second-model","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
		emitPlanFixture(w, 1, "", "ACTUAL_AGENT_SETTINGS_ASK_DONE")
	}))
	defer peer.Close()
	f := newNativePluginFixture(t)
	rg, err := exec.LookPath("rg")
	if err != nil {
		t.Fatal(err)
	}
	rgData, err := os.ReadFile(rg)
	if err != nil {
		t.Fatal(err)
	}
	rigPath := filepath.Join(f.home, "private-rg")
	if err = os.WriteFile(rigPath, rgData, 0700); err != nil {
		t.Fatal(err)
	}
	f.n.Environment["CC_HAHA_RIPGREP_PATH"] = rigPath

	settingsPath := filepath.Join(f.config, "settings.json")
	f.write(t, settingsPath, `{"agent":"reader","model":"sonnet","enabledPlugins":{"sample@mkt":false},"permissions":{"allow":["Read"],"deny":["Write"]},"future":{"retain":true}}`)
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/private\n")
	f.write(t, filepath.Join(f.cwd, ".claude", "agents", "reader.md"), "---\nname: reader\ndescription: Actual reader\ntools: Bash\nmodel: inherit\npermissionMode: dontAsk\n---\nACTUAL_AGENT_SETTINGS_PROMPT")
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-only-loopback-agent"
	f.n.Environment["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "fixture-first-model"
	f.n.Environment["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "fixture-second-model"
	f.n.Environment["CLAUDE_CODE_DISABLE_FAST_MODE"] = "1"
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Interactive: true, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Tools: []string{"Read"}, Sources: DefaultState(), MaxTurns: 5, PermissionMode: "default"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if m.nativeMainThreadAgentType() != "reader" {
		t.Fatal("actual project Agent discovery did not resolve", m.nativeMainThreadAgentType())
	}
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	defer m.ShutdownSession(context.Background())
	foreign := cooperative.New()
	defer foreign.Close()
	if err = foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if m.nativeMainAgentSettingsBound(scope) {
			t.Fatal("foreign owner acquired main Agent consumer")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if !m.nativeMainAgentSettingsBound(scope) {
			t.Fatal("actual owner main Agent consumer absent")
		}
		unbound := &MainPrint{Owner: m.Owner, options: MainPrintOptions{Native: f.n}, Controller: m.Controller, agents: m.agents, mainThreadAgent: m.mainThreadAgent, agentChild: true, detachedConstructed: true}
		if unbound.nativeMainAgentSettingsBound(scope) {
			t.Fatal("unbound child acquired main Agent consumer")
		}
		wrong := &MainPrint{Owner: foreign, options: MainPrintOptions{Native: f.n}, Controller: m.Controller, agents: m.agents, mainThreadAgent: m.mainThreadAgent}
		unbound.options.DetachedAgent = &NativeDetachedAgentOptions{
			authParent: wrong,
			transferSettings: func(*MainPrint, *cooperative.Scope) (sdkhost.NativeSettingsTransfer, error) {
				return sdkhost.NativeSettingsTransfer{}, nil
			},
			liveSettings: func(*MainPrint, *cooperative.Scope) (*protocol.JSONObject, protocol.JSONObject, error) {
				return nil, protocol.JSONObject{}, nil
			},
			bindTasks: func(*MainPrint) *builtintools.BackgroundTaskBindings { return nil },
		}
		if unbound.nativeMainAgentSettingsBound(scope) {
			t.Fatal("wrong parent owner/bootstrap acquired main Agent consumer")
		}
		unbound.options.DetachedAgent.authParent = m
		if unbound.nativeMainAgentSettingsBound(scope) {
			t.Fatal("same-owner detached fabrication acquired main Agent consumer")
		}
		settings, permission, err := m.Controller.SDKSettingsOnOwner(scope)
		if err != nil {
			return err
		}
		if settings.String("agent") != "reader" || permission.String("mode") != "default" {
			t.Fatal("Agent bypassed main invocation permissions")
		}
		if err := sdkpermissions.ValidateNative(settings); err == nil {
			t.Fatal("generic validator silently admitted active agent")
		}
		guarded := settings.Clone()
		guarded.Set("statusLine", rawMain(map[string]any{"type": "command", "command": "private-never-run"}))
		if err := sdkpermissions.ValidateNativeWithBindings(&guarded, true, true, false, false, true, true, true, true, true, true, true, true, true, true, true, true); err == nil {
			t.Fatal("unrelated active-setting guard widened")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	turn := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": text})})
	}
	if err = m.RunTurn(ctx, turn("actual first selected Agent ASK")); err != nil {
		t.Fatal(err)
	}
	f.write(t, settingsPath, `{"agent":"reader","model":"sonnet","enabledPlugins":{"sample@mkt":false},"permissions":{"allow":["Read"],"deny":["Write","Edit"]},"future":{"retain":true},"effortLevel":"medium"}`)
	// Invoke the real notifier callback with its actual changed file. This is the
	// same settings cache/hook/provenance/Controller/permission producer used by FS.
	if err = m.nativeSettingsFileChanged(ctx, "userSettings", settingsPath); err != nil {
		t.Fatal("actual agent settings reload", err)
	}
	picker, err := m.NativeModelPicker(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	model := "opus"
	if _, err = m.ApplyNativeModelPicker(ctx, picker.ID, tui.ModelPickerResult{Model: &model}); err != nil {
		t.Fatal("actual typed model validation with agent settings", err)
	}
	if err = m.RunTurn(ctx, turn("actual next selected Agent ASK after reload/model")); err != nil {
		t.Fatal(err)
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		settings, permission, err := m.Controller.SDKSettingsOnOwner(scope)
		if err != nil {
			return err
		}
		if settings.String("agent") != "reader" || permission.String("mode") != "default" {
			t.Fatal("Agent context changed during model/permission reload")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	ask := []protocol.JSONObject{}
	for _, body := range bodies {
		var row protocol.JSONObject
		_ = row.UnmarshalJSON(body)
		if bytes.Contains(row.Get("messages"), []byte("actual first selected Agent ASK")) {
			ask = append(ask, row)
		}
	}
	if len(ask) != 2 || ask[0].String("model") != "fixture-first-model" || ask[1].String("model") != "fixture-second-model" {
		t.Fatal("actual model snapshots", len(ask), string(rawMain(ask)))
	}
	for _, row := range ask {
		if !bytes.Contains(row.Get("system"), []byte("ACTUAL_AGENT_SETTINGS_PROMPT")) {
			t.Fatal("selected Agent prompt lost at request", string(row.Get("system")))
		}
	}
	final, err := os.ReadFile(settingsPath)
	var persisted protocol.JSONObject
	if parseErr := persisted.UnmarshalJSON(final); err == nil {
		err = parseErr
	}
	future, _ := persisted.Object("future")
	if err != nil || string(future.Get("retain")) != "true" || persisted.String("agent") != "reader" {
		t.Fatal("model persist changed original agent/unknown settings", err, string(final))
	}
}
