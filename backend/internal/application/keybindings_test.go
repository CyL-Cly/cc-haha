package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalBindingAction(bindings []tui.Keybinding, key string) tui.BindingResolution {
	stroke := tui.ParseKeystroke(key)
	return tui.ResolveKey(&stroke, []string{"Chat", "Global"}, bindings)
}

func TestActualSessionKeybindingWarningRepairAndCloseJoinsOwnedWatcher(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("keybinding lifecycle opened a model request")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.KeybindingCustomizationEnabled = func() bool { return true }
	file := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "keybindings.json")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"bindings":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	changed := make(chan []tui.Keybinding, 4)
	_, remove, err := session.SubscribeKeybindings(context.Background(), func(bindings []tui.Keybinding) { changed <- bindings })
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	if summary := session.KeybindingWarningsSummary(); summary != "Found 1 keybinding error · /doctor for details" {
		t.Fatal("actual Session did not expose Source validation warning", summary)
	}
	updated := []byte(`{"future":19,"bindings":[{"context":"Chat","bindings":{"alt+j":"command:skills"}}]}`)
	replacement := file + ".replacement"
	if err := os.WriteFile(replacement, updated, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, file); err != nil {
		t.Fatal(err)
	}
	select {
	case bindings := <-changed:
		if result := terminalBindingAction(bindings, "alt+j"); result.Action != "command:skills" || session.KeybindingWarningsSummary() != "" {
			t.Fatal("fixed config did not clear warning and replace its binding", result, session.KeybindingWarningsSummary())
		}
	case <-time.After(4 * time.Second):
		t.Fatal("actual Session watcher did not reach live subscriber")
	}
	state := session.terminalKeybindingState()
	state.mu.Lock()
	done := state.done
	state.mu.Unlock()
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	default:
		t.Fatal("actual Session.Close returned with owned file watcher still alive")
	}
	if _, _, err := session.SubscribeKeybindings(context.Background(), nil); err != context.Canceled {
		t.Fatal("closed Session accepted a later keybinding subscriber", err)
	}
	if contents, _ := os.ReadFile(file); string(contents) != string(updated) {
		t.Fatal("read-only Session cleanup modified user config")
	}
}

func TestKeybindingTrustedFalseGateNeverReadsOrWatchesUserConfig(t *testing.T) {
	for _, enabled := range []func() bool{nil, func() bool { return false }} {
		o := nativeSessionOptions(t, "http://127.0.0.1:1")
		o.KeybindingCustomizationEnabled = enabled
		session, err := NewSession(context.Background(), o)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close(context.Background())
		state := session.terminalKeybindingState()
		state.readFile = func(string) ([]byte, error) { t.Fatal("false trusted gate read user configuration"); return nil, nil }
		bindings, remove, err := session.SubscribeKeybindings(context.Background(), func([]tui.Keybinding) { t.Error("disabled watcher emitted a binding change") })
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(bindings, tui.DefaultKeybindings("macos", true, tui.KeybindingFeatures{})) || state.cancel != nil {
			t.Fatal("disabled customization changed defaults or created a watcher")
		}
		remove()
		remove()
		if err := session.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestKeybindingWatcherActualAtomicReplaceDeleteAndNextSubscription(t *testing.T) {
	config := t.TempDir()
	file := filepath.Join(config, "keybindings.json")
	initial := []byte(`{"future":{"retain":17},"bindings":[{"context":"Chat","bindings":{"alt+p":null,"ctrl+k ctrl+p":"chat:modelPicker"}}]}`)
	if err := os.WriteFile(file, initial, 0600); err != nil {
		t.Fatal(err)
	}
	state := &terminalKeybindingsState{path: file, platform: "macos", enabled: func() bool { return true }, readFile: os.ReadFile, listeners: make(map[uint64]terminalKeybindingListener)}
	session := &Session{terminalKeybindings: state}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changed := make(chan []tui.Keybinding, 8)
	bindings, remove, err := session.SubscribeKeybindings(ctx, func(bindings []tui.Keybinding) { changed <- bindings })
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	if result := terminalBindingAction(bindings, "alt+p"); result.Type != "unbound" {
		t.Fatal("null unbinding was lost", result)
	}
	first := tui.ParseKeystroke("ctrl+k")
	pending := tui.ResolveChord(&first, []string{"Chat"}, bindings, nil)
	last := tui.ParseKeystroke("ctrl+p")
	if pending.Type != "chord_started" || tui.ResolveChord(&last, []string{"Chat"}, bindings, pending.Pending).Action != "chat:modelPicker" {
		t.Fatal("custom chord did not reach existing resolver", pending)
	}
	await := func() []tui.Keybinding {
		t.Helper()
		select {
		case bindings := <-changed:
			return bindings
		case <-time.After(4 * time.Second):
			t.Fatal("real keybinding file event did not reach subscribed consumer")
			return nil
		}
	}
	updated := []byte(`{"future":{"retain":19},"bindings":[{"context":"Chat","bindings":{"ctrl+p":"command:help"}}]}`)
	temporary := filepath.Join(config, "replacement.json")
	if err := os.WriteFile(temporary, updated, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, file); err != nil {
		t.Fatal(err)
	}
	if result := terminalBindingAction(await(), "ctrl+p"); result.Action != "command:help" {
		t.Fatal("atomic update did not hot reload", result)
	}
	if contents, _ := os.ReadFile(file); string(contents) != string(updated) {
		t.Fatal("read-only loader rewrote unknown user fields")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if defaults := await(); !reflect.DeepEqual(defaults, state.defaults().bindings) {
		t.Fatal("delete did not reset defaults")
	}
	state.mu.Lock()
	done := state.done
	state.mu.Unlock()
	remove()
	select {
	case <-done:
	default:
		t.Fatal("unsubscribe returned before watcher and pending timer joined")
	}
	if err := os.WriteFile(file, updated, 0600); err != nil {
		t.Fatal(err)
	}
	next, closeNext, err := session.SubscribeKeybindings(context.Background(), func([]tui.Keybinding) {})
	if err != nil {
		t.Fatal(err)
	}
	defer closeNext()
	if terminalBindingAction(next, "ctrl+p").Action != "command:help" {
		t.Fatal("next subscription inherited stale deleted-file cache")
	}
	session.closeTerminalKeybindings()
	select {
	case bindings := <-changed:
		t.Fatal("retired subscriber received a later generation", bindings)
	default:
	}
}

func TestKeybindingEnabledMissingParentHasNoWatcherAndMalformedReloadUsesDefaults(t *testing.T) {
	state := &terminalKeybindingsState{path: filepath.Join(t.TempDir(), "missing-parent", "keybindings.json"), platform: "macos", enabled: func() bool { return true }, readFile: os.ReadFile, listeners: make(map[uint64]terminalKeybindingListener)}
	session := &Session{terminalKeybindings: state}
	_, remove, err := session.SubscribeKeybindings(context.Background(), nil)
	if err != nil || state.cancel != nil {
		t.Fatal("watcher created missing user directory", err)
	}
	remove()
	state.readFile = func(string) ([]byte, error) { return []byte(`{"bindings":`), nil }
	if result := state.load(true); len(result.warnings) != 0 {
		t.Fatal("original synchronous parse fallback acquired an invented warning", result.warnings)
	}
	if result := state.load(false); len(result.warnings) != 1 || result.warnings[0].Type != "parse_error" || !reflect.DeepEqual(result.bindings, state.defaults().bindings) {
		t.Fatal("malformed reload did not fall back and retain its warning", result)
	}
	state.readFile = func(string) ([]byte, error) {
		return []byte(`{"bindings":[{"context":"Chat","bindings":{"ctrl+k":"command:help","ctrl+k":"command:tasks"}},{"context":"Chat","bindings":{"control+k":null}}]}`), nil
	}
	result := state.load(true)
	if terminalBindingAction(result.bindings, "ctrl+k").Type != "unbound" || len(result.warnings) != 2 {
		warnings, _ := json.Marshal(result.warnings)
		t.Fatal("duplicate/conflict precedence or warnings differ", string(warnings))
	}
}
