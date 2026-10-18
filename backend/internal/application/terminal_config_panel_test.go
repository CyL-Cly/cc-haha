package application

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTerminalConfigImmediateNativeConsumerCommitAndEscapeRestore(t *testing.T) {
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests.Add(1)
		if strings.Contains(string(body), "PRIVATE_OLD_PROJECT_CLAUDE") || strings.Contains(string(body), "PRIVATE_PROJECT_AGENTS") {
			t.Error("project instruction cache survived managed-only config", string(body))
		}
		if strings.Contains(string(obj(body).Get("tools")), `"name":"Workflow"`) {
			t.Error("disabled workflows survived native tool consumer")
		}
		response(w, "config-next", "", nil, "CONFIG_SHARED_NATIVE_NEXT")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Arguments.SystemPrompt = ""
	o.Arguments.Tools = nil
	for name, text := range map[string]string{"CLAUDE.md": "PRIVATE_OLD_PROJECT_CLAUDE", "AGENTS.md": "PRIVATE_PROJECT_AGENTS"} {
		if err := os.WriteFile(filepath.Join(o.WorkDir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	initialUser := `{"futureUser":{"unknown":true},"pluginConfigs":{"agents-md@builtin":{"options":{"instructionFiles":"claude-md-or-agents-md","projectInstructions":"both","futureOption":"preserved"}}}}`
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(initialUser), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(o.WorkDir, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.WorkDir, ".claude", "settings.local.json"), []byte(`{"futureLocal":23}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var panel *tui.LocalPanel
	var preferences *tui.TerminalPreferences
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		if event.Preferences != nil {
			copy := *event.Preferences
			preferences = &copy
		}
		return nil
	}
	open := func() {
		t.Helper()
		if handled, err := s.InvokeTerminalCommand(context.Background(), "settings", "", emit); !handled || err != nil {
			t.Fatal(handled, err)
		}
	}
	act := func(name, row string) {
		t.Helper()
		if err := s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: name, Row: row}, emit); err != nil || panel.Notice != "" {
			t.Fatal(err, panel.Notice)
		}
	}
	open()
	act("next-config", "verbose")
	if preferences == nil || !preferences.Verbose {
		t.Fatal("live verbose UI consumer missing", preferences)
	}
	act("next-config", "instructionFiles")
	act("next-config", "instructionFiles")
	act("next-config", "spinnerTipsEnabled")
	act("next-config", "prefersReducedMotion")
	act("next-config", "enableWorkflows")
	disabled, err := s.WorkflowKeywordEnabled(context.Background())
	if err != nil || disabled {
		t.Fatal("native workflow runtime still enabled", disabled, err)
	}
	act("close", "")
	snapshot, err := s.process.TerminalConfigState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(obj(snapshot.Get("global")).Get("verbose")) != "false" || snapshot.String("instructionFiles") != "claude-md-and-agents-md" {
		t.Fatal("Escape snapshot not restored", string(raw(snapshot)))
	}
	user := obj(snapshot.Get("userSettings"))
	options := obj(obj(obj(user.Get("pluginConfigs")).Get("agents-md@builtin")).Get("options"))
	if options.String("projectInstructions") != "both" || options.String("futureOption") != "preserved" || string(user.Get("futureUser")) != `{"unknown":true}` {
		t.Fatal("old nested fixture shape changed", string(raw(user)))
	}
	local := obj(snapshot.Get("localSettings"))
	if local.Get("spinnerTipsEnabled") != nil || local.Get("prefersReducedMotion") != nil || string(local.Get("futureLocal")) != "23" {
		t.Fatal("undefined restore or future local lost", string(raw(local)))
	}
	if requests.Load() != 0 {
		t.Fatal("local config requested model", requests.Load())
	}
	open()
	act("next-config", "instructionFiles")
	act("next-config", "enableWorkflows")
	act("commit-config", "")
	if err = s.Submit(context.Background(), tui.Input{Text: "Use actual committed config"}, nil); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal(requests.Load())
	}
}

func TestTerminalThemeNativePersistenceAndConfigValidation(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Interactive = true
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var panel *tui.LocalPanel
	var preferences *tui.TerminalPreferences
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		if event.Preferences != nil {
			copy := *event.Preferences
			preferences = &copy
		}
		return nil
	}
	if _, err = s.InvokeTerminalCommand(context.Background(), "theme", "", emit); err != nil {
		t.Fatal(err)
	}
	if len(panel.Rows) != 6 {
		t.Fatal(panel)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "theme-select", Row: "light-ansi"}, emit); err != nil || preferences == nil || preferences.Theme != "light-ansi" {
		t.Fatal(err, preferences)
	}
	data, err := os.ReadFile(s.process.Global.Path())
	if err != nil || obj(data).String("theme") != "light-ansi" {
		t.Fatal(err, string(data))
	}
	for _, bad := range []struct {
		key   string
		value json.RawMessage
	}{{"unknown", []byte("true")}, {"theme", raw("invalid")}, {"verbose", raw("not bool")}, {"language", []byte("{}")}, {"outputStyle", raw("nonexistent-style")}, {"language", []byte("not JSON")}} {
		if err = s.process.SetTerminalConfigSetting(context.Background(), bad.key, bad.value); err == nil {
			t.Fatal("unvalidated config", bad)
		}
	}
	for _, pair := range []struct {
		key   string
		value json.RawMessage
	}{{"thinkingEnabled", []byte("false")}, {"thinkingEnabled", []byte("true")}, {"workflowKeywordTriggerEnabled", []byte("false")}, {"workflowKeywordTriggerEnabled", []byte("true")}, {"defaultPermissionMode", raw("plan")}, {"language", raw("中文")}, {"language", raw("")}, {"outputStyle", raw("default")}, {"workflowSizeGuideline", raw("small")}} {
		if err = s.process.SetTerminalConfigSetting(context.Background(), pair.key, pair.value); err != nil {
			t.Fatal(pair, err)
		}
	}
}
